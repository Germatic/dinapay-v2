package datapolicy

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestEvaluateObservesAdministrativePolicyAndEnforcesConnectorRequirement(t *testing.T) {
	var missing []string
	client := New("http://example.invalid", "token", "sandbox", func(_, _, path, mode, violation string) { missing = append(missing, path+":"+mode+":"+violation) })
	client.current.Store(&snapshot{Version: "v1", Environment: "sandbox", Policies: []policy{
		{ID: "global", Status: "active", EnforcementMode: "observe", Context: policyContext{ScopeType: "global", Environment: "sandbox", Resource: "payout", Country: "VE"}, Rules: []rule{{Path: "remitter.firstName", Presence: "required"}}},
		{ID: "merchant", Status: "active", EnforcementMode: "observe", Context: policyContext{ScopeType: "merchant", ScopeID: "mrc_1", Environment: "sandbox", Resource: "payout", Country: "VE"}, Rules: []rule{{Path: "remitter.firstName", Presence: "optional"}}},
	}, ConnectorRequirements: []connectorRequirements{{Provider: "insular", Operation: "payout", Countries: []string{"VE"}, SourceCurrencies: []string{"USD"}, Rails: []string{"ve_bank_account"}, Rules: []rule{{Path: "destination.rail.accountNumber", Presence: "conditional", When: map[string]any{"rail": "ve_bank_account"}}}}}})
	err := client.Evaluate(context.Background(), core.DataPolicyObservation{Resource: "payout", AccountID: "acct_1", MerchantID: "mrc_1", Provider: "insular", Country: "VE", Currency: "USD", Rail: "ve_bank_account", Data: map[string]any{"remitter": map[string]any{}, "destination": map[string]any{"rail": map[string]any{}}}})
	var required *core.MissingRequiredDataError
	if !errors.As(err, &required) || !slices.Equal(required.Fields, []string{"destination.rail.accountNumber"}) {
		t.Fatalf("error=%#v", err)
	}
	want := []string{"remitter.firstName:observe:missing", "destination.rail.accountNumber:enforce:missing"}
	if !sameStrings(missing, want) {
		t.Fatalf("missing=%v want=%v", missing, want)
	}
}

func TestEvaluateRejectsForbiddenFieldsOnlyInEnforceMode(t *testing.T) {
	var violations []string
	client := New("http://example.invalid", "token", "sandbox", func(_, _, path, mode, violation string) {
		violations = append(violations, path+":"+mode+":"+violation)
	})
	client.current.Store(&snapshot{Version: "v3", Environment: "sandbox", Policies: []policy{
		{ID: "merchant", Status: "active", EnforcementMode: "enforce", Context: policyContext{ScopeType: "merchant", ScopeID: "mrc_1", Environment: "sandbox", Resource: "payment", Country: "AR"}, Rules: []rule{
			{Path: "customer.documentNumber", Presence: "forbidden"},
		}},
	}})
	err := client.Evaluate(context.Background(), core.DataPolicyObservation{Resource: "payment", MerchantID: "mrc_1", Country: "AR", Data: map[string]any{"customer": map[string]any{"documentNumber": "123"}}})
	var forbidden *core.ForbiddenDataError
	if !errors.As(err, &forbidden) || !slices.Equal(forbidden.Fields, []string{"customer.documentNumber"}) {
		t.Fatalf("error=%#v", err)
	}
	if !slices.Equal(violations, []string{"customer.documentNumber:enforce:forbidden"}) {
		t.Fatalf("violations=%v", violations)
	}
	client.current.Load().Policies[0].EnforcementMode = "observe"
	if err := client.Evaluate(context.Background(), core.DataPolicyObservation{Resource: "payment", MerchantID: "mrc_1", Country: "AR", Data: map[string]any{"customer": map[string]any{"documentNumber": "123"}}}); err != nil {
		t.Fatalf("observe mode rejected forbidden field: %v", err)
	}
}

func TestEvaluateRejectsOnlyEnforcedMissingFieldsInStableOrder(t *testing.T) {
	client := New("http://example.invalid", "token", "sandbox", nil)
	client.current.Store(&snapshot{Version: "v2", Environment: "sandbox", Policies: []policy{
		{ID: "merchant", Status: "active", EnforcementMode: "enforce", Context: policyContext{ScopeType: "merchant", ScopeID: "mrc_1", Environment: "sandbox", Resource: "payment", Country: "AR"}, Rules: []rule{
			{Path: "customer.lastName", Presence: "required"},
			{Path: "customer.firstName", Presence: "required"},
		}},
	}})
	err := client.Evaluate(context.Background(), core.DataPolicyObservation{Resource: "payment", MerchantID: "mrc_1", Country: "AR", Data: map[string]any{"customer": map[string]any{}}})
	var missing *core.MissingRequiredDataError
	if !errors.As(err, &missing) {
		t.Fatalf("error=%#v", err)
	}
	want := []string{"customer.firstName", "customer.lastName"}
	if !slices.Equal(missing.Fields, want) {
		t.Fatalf("fields=%v want=%v", missing.Fields, want)
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
