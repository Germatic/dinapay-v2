package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/adapters/dinacore"
	"github.com/Germatic/dinapay-v2/internal/adapters/httpclient"
	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/adapters/postgres"
	"github.com/Germatic/dinapay-v2/internal/adapters/static"
	"github.com/Germatic/dinapay-v2/internal/adapters/webhooks"
	"github.com/Germatic/dinapay-v2/internal/app"
	"github.com/Germatic/dinapay-v2/internal/core"
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
		store, auth = pgStore, postgres.NewAuth(pool)
	} else {
		staticAuth := static.NewAuth(os.Getenv("API_KEYS"))
		store, auth = memory.NewStore(), staticAuth
		slog.Warn("using in-memory persistence; data will not survive restart")
	}
	connectors := httpclient.NewConnectors(connectorURLs, os.Getenv("SERVICE_TOKEN"))
	payments := app.NewPayments(
		httpclient.NewRouter(env("ROUTER_URL", "http://localhost:8091"), os.Getenv("SERVICE_TOKEN")),
		connectors, store, auth,
		env("CHECKOUT_BASE_URL", "https://checkout.demo.dinaria.com"),
	)
	var ledger core.Ledger
	if os.Getenv("DINACORE_BASE_URL") != "" && os.Getenv("DINACORE_API_KEY") != "" {
		ledger = dinacore.New(os.Getenv("DINACORE_BASE_URL"), os.Getenv("DINACORE_API_KEY"))
	}
	nativeRefunds, _ := store.(core.RefundStore)
	refunds := app.NewRefunds(store, nativeRefunds, httpclient.NewLegacyRefundClient(env("LEGACY_DINAPAY_URL", "http://localhost:8090")), connectors, ledger)
	if nativeRefunds != nil && ledger != nil {
		go refunds.Run(context.Background())
	}
	events := app.NewProviderEvents(store)
	if pool != nil && os.Getenv("DINACORE_BASE_URL") != "" && os.Getenv("DINACORE_API_KEY") != "" {
		go dinacore.NewOutboxWorker(pool, ledger.(*dinacore.Client)).Run(context.Background())
	}
	if pool != nil {
		go webhooks.NewWorker(pool, envInt("WEBHOOK_DISPATCH_CONCURRENCY", 8)).Run(context.Background())
	}
	server := &http.Server{Addr: ":" + env("PORT", "8090"), Handler: httpapi.New(payments, refunds, events, auth, os.Getenv("SERVICE_TOKEN")), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	slog.Info("dinapay-v2 starting", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 1 {
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
