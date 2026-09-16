package postgres

import "testing"

func TestPayoutFailurePreservesStructuredProviderErrors(t *testing.T) {
	raw := `{"code":"provider_rejected","message":"Rechazo técnico del banco (VE01).","provider":"insular","providerErrors":[{"providerCode":"627","message":"Rechazo técnico del banco (VE01)."}]}`
	failure := payoutFailure([]byte(raw), "")
	items, ok := failure["providerErrors"].([]any)
	if failure["code"] != "provider_rejected" || failure["message"] != "Rechazo técnico del banco (VE01)." || !ok || len(items) != 1 {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestPayoutFailureKeepsLegacyText(t *testing.T) {
	failure := payoutFailure(nil, "provider rejected payout")
	if failure["code"] != "payout_failed" || failure["message"] != "provider rejected payout" {
		t.Fatalf("failure=%#v", failure)
	}
}
