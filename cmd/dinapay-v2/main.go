package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/adapters/arsalias"
	"github.com/Germatic/dinapay-v2/internal/adapters/datapolicy"
	"github.com/Germatic/dinapay-v2/internal/adapters/dinacore"
	"github.com/Germatic/dinapay-v2/internal/adapters/httpclient"
	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/adapters/postgres"
	"github.com/Germatic/dinapay-v2/internal/adapters/screening"
	"github.com/Germatic/dinapay-v2/internal/adapters/static"
	"github.com/Germatic/dinapay-v2/internal/adapters/webhooks"
	"github.com/Germatic/dinapay-v2/internal/app"
	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/Germatic/dinapay-v2/internal/observability"
	"github.com/Germatic/dinapay-v2/internal/reconciliation"
	"github.com/Germatic/dinapay-v2/internal/transport/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	connectorURLs := map[string]string{}
	for _, entry := range strings.Split(os.Getenv("CONNECTORS"), ",") {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			connectorURLs[parts[0]] = parts[1]
		}
	}
	var store core.PaymentStore
	var dashboardReader core.DashboardPaymentReader
	var auth core.Authenticator
	var pool *pgxpool.Pool
	if dbURL := os.Getenv("DB_URL"); dbURL != "" {
		var err error
		pool, err = pgxpool.New(context.Background(), dbURL)
		if err != nil {
			slog.Error("database configuration", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		pgStore := postgres.NewStore(pool)
		if err := pgStore.Migrate(context.Background()); err != nil {
			slog.Error("database migration", "error", err)
			os.Exit(1)
		}
		store, auth = pgStore, postgres.NewAuth(pool, env("DINARIA_ENVIRONMENT", "sandbox"))
		dashboardReader = pgStore
	} else {
		staticAuth := static.NewAuth(os.Getenv("API_KEYS"))
		store, auth = memory.NewStore(), staticAuth
		slog.Warn("using in-memory persistence; data will not survive restart")
	}
	connectors := httpclient.NewConnectors(connectorURLs, os.Getenv("SERVICE_TOKEN"))
	payments := app.NewPayments(
		httpclient.NewRouter(env("ROUTER_URL", "http://localhost:8091"), os.Getenv("SERVICE_TOKEN")),
		connectors, store, auth,
		os.Getenv("CHECKOUT_BASE_URL"),
	)
	if baseURL := strings.TrimSpace(os.Getenv("SCREENING_URL")); baseURL != "" {
		failOpen := strings.EqualFold(env("SCREENING_FAIL_OPEN", "true"), "true")
		screeningClient := screening.New(baseURL, os.Getenv("SCREENING_INTERNAL_KEY"), env("DINARIA_ENVIRONMENT", "sandbox"), envDuration("SCREENING_TIMEOUT", 3*time.Second), failOpen, observability.ObserveScreening)
		payments.WithScreeningGate(screeningClient)
		slog.Info("screening enabled", "fail_open", failOpen)
	} else {
		slog.Info("screening disabled")
	}
	var dataPolicyObserver core.DataPolicyObserver
	if baseURL, token := os.Getenv("CONTROL_PLANE_URL"), os.Getenv("CONTROL_PLANE_RUNTIME_TOKEN"); baseURL != "" && token != "" {
		client := datapolicy.New(baseURL, token, env("DINARIA_ENVIRONMENT", "sandbox"), observability.ObserveDataPolicyViolation)
		dataPolicyObserver = client
		payments.WithDataPolicyObserver(client)
		go client.Run(context.Background(), envDuration("DATA_POLICY_REFRESH_INTERVAL", 30*time.Second))
	} else {
		slog.Warn("data policy observation disabled; control plane configuration is incomplete")
	}
	var ledger core.Ledger
	var ledgerClient *dinacore.Client
	if os.Getenv("DINACORE_BASE_URL") != "" && os.Getenv("DINACORE_API_KEY") != "" {
		ledgerClient = dinacore.New(os.Getenv("DINACORE_BASE_URL"), os.Getenv("DINACORE_API_KEY"))
		ledger = ledgerClient
	}
	nativeRefunds, _ := store.(core.RefundStore)
	refunds := app.NewRefunds(store, nativeRefunds, httpclient.NewLegacyRefundClient(env("LEGACY_DINAPAY_URL", "http://localhost:8090")), connectors, ledger)
	refunds.WithFailureObserver(observability.ObserveProviderFailure)
	if nativeRefunds != nil && ledger != nil {
		go refunds.Run(context.Background())
	}
	nativePayouts, _ := store.(core.PayoutStore)
	var payouts *app.Payouts
	if nativePayouts != nil {
		var payoutLedger core.PayoutLedger
		if ledgerClient != nil {
			payoutLedger = ledgerClient
		}
		payouts = app.NewPayouts(nativePayouts, httpclient.NewRouter(env("ROUTER_URL", "http://localhost:8091"), os.Getenv("SERVICE_TOKEN")), connectors, payoutLedger, auth)
		if dataPolicyObserver != nil {
			payouts.WithDataPolicyObserver(dataPolicyObserver)
		}
		if resolver, err := arsalias.NewCoinag(arsalias.CoinagConfig{
			BaseURL: os.Getenv("COINAG_BASE_URL"), TokenURL: os.Getenv("COINAG_TOKEN_URL"),
			ClientID: os.Getenv("COINAG_CLIENT_ID"), ClientSecret: os.Getenv("COINAG_CLIENT_SECRET"),
			Username: os.Getenv("COINAG_USERNAME"), Password: os.Getenv("COINAG_PASSWORD"),
		}); err == nil {
			payouts.WithARSAliasResolver(resolver)
		} else {
			slog.Warn("ARS alias payouts disabled", "error", err)
		}
		if payoutLedger != nil {
			go payouts.Run(context.Background())
		}
	}
	events := app.NewProviderEvents(store)
	if pool != nil && ledgerClient != nil {
		go dinacore.NewOutboxWorker(pool, ledgerClient).Run(context.Background())
		if strings.EqualFold(strings.TrimSpace(os.Getenv("RECONCILIATION_MODE")), "observe") {
			go reconciliation.NewPayoutWorker(pool, ledgerClient, envInt("RECONCILIATION_BATCH_SIZE", 50), envDuration("RECONCILIATION_INTERVAL", 10*time.Second)).Run(context.Background())
			slog.Info("payout reconciliation enabled", "mode", "observe")
		} else {
			slog.Info("payout reconciliation disabled", "mode", env("RECONCILIATION_MODE", "off"))
		}
	}
	if pool != nil {
		go webhooks.NewWorker(pool, envInt("WEBHOOK_DISPATCH_CONCURRENCY", 8)).Run(context.Background())
		go collectMetrics(context.Background(), pool)
	}
	var webhookStore core.WebhookSubscriptionStore
	if pool != nil {
		webhookStore = postgres.NewStore(pool)
	}
	server := &http.Server{Addr: ":" + env("PORT", "8090"), Handler: httpapi.NewWithDashboardReader(payments, refunds, payouts, events, auth, os.Getenv("SERVICE_TOKEN"), dashboardReader, os.Getenv("DASHBOARD_READ_TOKEN"), webhookStore), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	slog.Info("dinapay-v2 starting", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func collectMetrics(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		var webhooks, webhookAge, ledger, ledgerAge, unknownPayouts, unknownPayoutAge, unknownRefunds, unknownRefundAge float64
		err := pool.QueryRow(ctx, `SELECT count(*)::float8,COALESCE(EXTRACT(EPOCH FROM now()-min(created_at)),0)::float8 FROM webhook_deliveries WHERE status='pending'`).Scan(&webhooks, &webhookAge)
		if err == nil {
			err = pool.QueryRow(ctx, `SELECT count(*)::float8,COALESCE(EXTRACT(EPOCH FROM now()-min(created_at)),0)::float8 FROM dinacore_balance_outbox WHERE sent=false`).Scan(&ledger, &ledgerAge)
		}
		if err == nil {
			err = pool.QueryRow(ctx, `SELECT count(*)::float8,COALESCE(EXTRACT(EPOCH FROM now()-min(creation_date)),0)::float8 FROM dinapay_v2_payouts WHERE status='provider_unknown'`).Scan(&unknownPayouts, &unknownPayoutAge)
		}
		if err == nil {
			err = pool.QueryRow(ctx, `SELECT count(*)::float8,COALESCE(EXTRACT(EPOCH FROM now()-min(creation_date)),0)::float8 FROM dinapay_v2_refunds WHERE status='provider_unknown'`).Scan(&unknownRefunds, &unknownRefundAge)
		}
		if err == nil {
			observability.SetPersistent(webhooks, webhookAge, ledger, ledgerAge, unknownPayouts, unknownPayoutAge, unknownRefunds, unknownRefundAge)
			var openFindings, criticalFindings, oldestFindingAge, reconciliationBacklog float64
			if reconciliationErr := pool.QueryRow(ctx, `SELECT count(*)::float8,count(*) FILTER(WHERE severity='critical')::float8,COALESCE(EXTRACT(EPOCH FROM now()-min(first_seen_at)),0)::float8 FROM dinapay_reconciliation_findings WHERE status='open'`).Scan(&openFindings, &criticalFindings, &oldestFindingAge); reconciliationErr == nil {
				if reconciliationErr = pool.QueryRow(ctx, `SELECT count(*)::float8 FROM dinapay_reconciliation_queue WHERE next_check_at<=now()`).Scan(&reconciliationBacklog); reconciliationErr == nil {
					observability.SetReconciliation(openFindings, criticalFindings, oldestFindingAge, reconciliationBacklog)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
