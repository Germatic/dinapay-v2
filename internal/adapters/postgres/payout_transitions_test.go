package postgres

import "testing"

func TestConfirmedPayoutCanEnterLateReversalCompensation(t *testing.T) {
	if !payoutOperationalTransitionAllowed("confirmed", "pending_compensation_reversed") {
		t.Fatal("confirmed payout must accept an authoritative late reversal")
	}
	if payoutOperationalTransitionAllowed("reversed", "pending_compensation_reversed") {
		t.Fatal("a terminal reversed payout must not be compensated again")
	}
}
