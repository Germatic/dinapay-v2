package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/adapters/httpclient"
	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/adapters/static"
	"github.com/Germatic/dinapay-v2/internal/app"
	"github.com/Germatic/dinapay-v2/internal/transport/httpapi"
)

func main() {
	connectorURLs := map[string]string{}
	for _, entry := range strings.Split(os.Getenv("CONNECTORS"), ",") {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			connectorURLs[parts[0]] = parts[1]
		}
	}
	payments := app.NewPayments(
		httpclient.NewRouter(env("ROUTER_URL", "http://localhost:8091"), os.Getenv("SERVICE_TOKEN")),
		httpclient.NewConnectors(connectorURLs, os.Getenv("SERVICE_TOKEN")), memory.NewStore(),
		env("CHECKOUT_BASE_URL", "https://checkout.demo.dinaria.com"),
	)
	server := &http.Server{Addr: ":" + env("PORT", "8090"), Handler: httpapi.New(payments, static.NewAuth(os.Getenv("API_KEYS"))), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	slog.Info("dinapay-v2 starting", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
