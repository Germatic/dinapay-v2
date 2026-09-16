package postgres

import (
	"testing"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestPayoutFailureRejectsProviderSpecificPublicShape(t *testing.T) {
	raw := `{"code":"provider_rejected","message":"Rechazo técnico del banco (VE01).","provider":"insular","providerErrors":[{"providerCode":"627","message":"Rechazo técnico del banco (VE01)."}]}`
	failure := payoutFailure([]byte(raw), "")
	if failure["code"] != string(contract.PayoutUnknownError) || failure["provider"] != nil || failure["providerErrors"] != nil {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestPayoutFailureKeepsLegacyText(t *testing.T) {
	failure := payoutFailure(nil, "provider rejected payout")
	if failure["code"] != string(contract.PayoutUnknownError) {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestNormalizePayoutEventSeparatesPublicAndProviderFailure(t *testing.T) {
	public := contract.NewPayout(contract.PayoutDestinationRejected)
	native := contract.ProviderFailure{Code: "609", Message: "native message"}
	gotPublic, gotNative := normalizePayoutEventFailure(core.ProviderEventData{Failure: &public, ProviderFailure: &native})
	if gotPublic.Code != string(contract.PayoutDestinationRejected) || gotPublic.Message == gotNative.Message || gotNative.Code != "609" {
		t.Fatalf("public=%#v native=%#v", gotPublic, gotNative)
	}
}
