package reconciliation

import (
	"testing"

	"github.com/Germatic/dinapay-v2/internal/adapters/dinacore"
)

func TestEvaluatePayoutConfirmed(t *testing.T) {
	p := payoutRecord{ID: "p1", AccountID: "account-1", MerchantID: "merchant-1", Status: "confirmed", Amount: "10.00", TotalDebit: "10.25", Currency: "ARS", BalanceDebited: true}
	entries := []dinacore.LedgerEntry{{ID: "e1", MerchantID: "account-1", Currency: "ARS", Amount: "-10.25000000", RefType: "payout", RefID: "p1"}}
	if findings := evaluatePayout(p, entries); len(findings) != 0 {
		t.Fatalf("findings=%#v", findings)
	}
}

func TestEvaluatePayoutNormalFailureReturnsPrincipalAndFee(t *testing.T) {
	p := payoutRecord{ID: "p1", AccountID: "account-1", MerchantID: "merchant-1", Status: "failed", Amount: "10.00", TotalDebit: "10.25", Currency: "ARS", BalanceDebited: true}
	entries := []dinacore.LedgerEntry{
		{ID: "e1", MerchantID: "account-1", Currency: "ARS", Amount: "-10.25", RefType: "payout", RefID: "p1"},
		{ID: "e2", MerchantID: "account-1", Currency: "ARS", Amount: "10.25", RefType: "payout_reservation_release", RefID: "p1"},
	}
	if findings := evaluatePayout(p, entries); len(findings) != 0 {
		t.Fatalf("findings=%#v", findings)
	}
}

func TestEvaluatePayoutLateReversalRetainsFee(t *testing.T) {
	p := payoutRecord{ID: "p1", AccountID: "account-1", MerchantID: "merchant-1", Status: "reversed", Amount: "10.00", TotalDebit: "10.25", Currency: "ARS", BalanceDebited: true}
	entries := []dinacore.LedgerEntry{
		{ID: "e1", MerchantID: "account-1", Currency: "ARS", Amount: "-10.25", RefType: "payout", RefID: "p1"},
		{ID: "e2", MerchantID: "account-1", Currency: "ARS", Amount: "10.00", RefType: "payout_reservation_release", RefID: "p1"},
	}
	if findings := evaluatePayout(p, entries); len(findings) != 0 {
		t.Fatalf("findings=%#v", findings)
	}
}

func TestEvaluatePayoutDetectsMissingAndWrongCompensation(t *testing.T) {
	p := payoutRecord{ID: "p1", AccountID: "account-1", MerchantID: "merchant-1", Status: "reversed", Amount: "10.00", TotalDebit: "10.25", Currency: "ARS", BalanceDebited: true}
	entries := []dinacore.LedgerEntry{{ID: "e1", MerchantID: "account-1", Currency: "ARS", Amount: "-10.25", RefType: "payout", RefID: "p1"}}
	findings := evaluatePayout(p, entries)
	if len(findings) != 1 || findings[0].RuleCode != "payout_compensation_missing" || findings[0].Difference != "10.00000000" {
		t.Fatalf("findings=%#v", findings)
	}
	entries = append(entries, dinacore.LedgerEntry{ID: "e2", MerchantID: "account-1", Currency: "ARS", Amount: "10.25", RefType: "payout_reservation_release", RefID: "p1"})
	findings = evaluatePayout(p, entries)
	if len(findings) != 1 || findings[0].RuleCode != "payout_compensation_mismatch" || findings[0].Difference != "0.25000000" {
		t.Fatalf("findings=%#v", findings)
	}
}

func TestEvaluatePayoutInsufficientBalanceNeedsNoLedgerEntry(t *testing.T) {
	p := payoutRecord{ID: "p1", AccountID: "account-1", MerchantID: "merchant-1", Status: "failed", Amount: "10.00", TotalDebit: "10.25", Currency: "ARS", BalanceDebited: false}
	if findings := evaluatePayout(p, nil); len(findings) != 0 {
		t.Fatalf("findings=%#v", findings)
	}
}
