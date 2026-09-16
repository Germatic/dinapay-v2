package postgres

import (
	"encoding/json"
	"testing"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
)

func TestRefundFailureUsesCanonicalCatalog(t *testing.T) {
	normalized := contract.NewRefund(contract.RefundRejected)
	raw, _ := json.Marshal(normalized)
	failure := refundFailure(raw, "native provider message")
	if failure["code"] != string(contract.RefundRejected) || failure["category"] != "refund" {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestRefundFailureRejectsNativePublicShape(t *testing.T) {
	raw := []byte(`{"code":"400612","message":"native provider message","provider":"binancepay"}`)
	failure := refundFailure(raw, "native provider message")
	if failure["code"] != string(contract.RefundUnknownError) || failure["provider"] != nil {
		t.Fatalf("failure=%#v", failure)
	}
}
