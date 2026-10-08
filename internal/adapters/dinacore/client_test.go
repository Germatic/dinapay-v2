package dinacore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLedgerEntriesUsesAuthenticatedEvidenceEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/balance/ledger/payout-1" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != "secret" {
			t.Fatalf("api key=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entries":[{"id":"entry-1","merchantId":"account-1","currency":"ARS","amount":"-10.25","refType":"payout","refId":"payout-1","createdAt":"2026-10-07T12:00:00Z"}]}`))
	}))
	defer server.Close()

	entries, err := New(server.URL, "secret").LedgerEntries(context.Background(), "payout-1")
	if err != nil {
		t.Fatalf("ledger entries: %v", err)
	}
	if len(entries) != 1 || entries[0].RefType != "payout" || entries[0].Amount != "-10.25" {
		t.Fatalf("entries=%#v", entries)
	}
}
