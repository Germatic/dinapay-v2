package postgres

import (
	"testing"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestNormalizePaymentFailureKeepsProviderEvidenceInternal(t *testing.T) {
	public := contract.NewPayment(contract.PaymentRejected)
	native := contract.ProviderFailure{Code: "NATIVE-51", Message: "native provider detail"}
	gotPublic, gotNative := normalizePaymentEventFailure(core.ProviderEventData{Failure: &public, ProviderFailure: &native}, "failed")
	if gotPublic.Code != string(contract.PaymentRejected) || gotPublic.Message == gotNative.Message || gotNative.Code != "NATIVE-51" {
		t.Fatalf("public=%#v native=%#v", gotPublic, gotNative)
	}
}

func TestExpiredPaymentGetsCatalogFailure(t *testing.T) {
	public, _ := normalizePaymentEventFailure(core.ProviderEventData{}, "expired")
	if public.Code != string(contract.PaymentExpired) || !contract.ValidPayment(public) {
		t.Fatalf("public=%#v", public)
	}
}
