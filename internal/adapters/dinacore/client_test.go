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

func TestNormalizeBalanceAmount(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "postgres scale padding", input: "29550.000000000000000000", want: "29550"},
		{name: "preserves meaningful decimals", input: "221296.995000000000000000", want: "221296.995"},
		{name: "eight decimals", input: "0.12345678", want: "0.12345678"},
		{name: "leading zeroes", input: "00012.3400", want: "12.34"},
		{name: "more than eight meaningful decimals", input: "1.123456789", wantErr: true},
		{name: "zero", input: "0.000000000000000000", wantErr: true},
		{name: "negative", input: "-1.00", wantErr: true},
		{name: "invalid", input: "12x.30", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeBalanceAmount(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeBalanceAmount(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeBalanceAmount(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
