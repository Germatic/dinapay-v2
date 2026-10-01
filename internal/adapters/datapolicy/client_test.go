package datapolicy

import (
	"context"
	"testing"

	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestObserveCombinesScopedAndConnectorRequirements(t *testing.T) {
	var missing []string
	client := New("http://example.invalid", "token", "sandbox", func(_, _, path, mode string) { missing = append(missing, path+":"+mode) })
	client.current.Store(&snapshot{Version: "v1", Environment: "sandbox", Policies: []policy{
		{ID: "global", Status: "active", EnforcementMode: "observe", Context: policyContext{ScopeType: "global", Environment: "sandbox", Resource: "payout", Country: "VE"}, Rules: []rule{{Path: "remitter.firstName", Presence: "required"}}},
		{ID: "merchant", Status: "active", EnforcementMode: "observe", Context: policyContext{ScopeType: "merchant", ScopeID: "mrc_1", Environment: "sandbox", Resource: "payout", Country: "VE"}, Rules: []rule{{Path: "remitter.firstName", Presence: "optional"}}},
	}, ConnectorRequirements: []connectorRequirements{{Provider: "insular", Operation: "payout", Countries: []string{"VE"}, SourceCurrencies: []string{"USD"}, Rails: []string{"ve_bank_account"}, Rules: []rule{{Path: "destination.rail.accountNumber", Presence: "conditional", When: map[string]any{"rail": "ve_bank_account"}}}}}})
	client.Observe(context.Background(), core.DataPolicyObservation{Resource: "payout", AccountID: "acct_1", MerchantID: "mrc_1", Provider: "insular", Country: "VE", Currency: "USD", Rail: "ve_bank_account", Data: map[string]any{"remitter": map[string]any{}, "destination": map[string]any{"rail": map[string]any{}}}})
	want := []string{"remitter.firstName:observe", "destination.rail.accountNumber:observe"}
	if !sameStrings(missing, want) {
		t.Fatalf("missing=%v want=%v", missing, want)
	}
}

func TestPresentHandlesNestedValues(t *testing.T) {
	data := map[string]any{"destination": map[string]any{"rail": map[string]any{"accountNumber": "123"}}}
	if !present(data, "destination.rail.accountNumber") || present(data, "destination.rail.bankCode") {
		t.Fatal("nested presence resolution failed")
	}
}

func sameStrings(a, b []string) bool {
	counts := map[string]int{}
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
	}
	for _, v := range counts {
		if v != 0 {
			return false
		}
	}
	return len(a) == len(b)
}
