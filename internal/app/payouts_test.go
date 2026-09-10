package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type payoutStoreStub struct{ transitions []refundTransition }

func (*payoutStoreStub) BeginPayout(context.Context, core.Principal, string, string, string) (core.Payout, bool, error) {
	return core.Payout{}, false, nil
}
func (*payoutStoreStub) CompletePayout(context.Context, core.Principal, string, string, core.CreatePayout, core.RouteDecision) (core.Payout, error) {
	return core.Payout{}, nil
}
func (*payoutStoreStub) ReleasePayout(context.Context, string, string) error { return nil }
func (*payoutStoreStub) GetPayout(context.Context, core.Principal, string) (core.Payout, error) {
	return core.Payout{}, core.ErrNotFound
}
func (*payoutStoreStub) ListPayouts(context.Context, core.Principal, core.PayoutListOptions) (core.PayoutPage, error) {
	return core.PayoutPage{}, nil
}
func (*payoutStoreStub) ClaimPayouts(context.Context, int) ([]core.Payout, error) { return nil, nil }
func (s *payoutStoreStub) TransitionPayout(_ context.Context, _ string, next string, fields map[string]any) (core.Payout, error) {
	s.transitions = append(s.transitions, refundTransition{expected: fields["expectedStatus"].(string), next: next, fields: fields})
	return core.Payout{OperationalStatus: next}, nil
}

type payoutConnectorStub struct {
	create        core.ProviderPayout
	createErr     error
	get           core.ProviderPayout
	getErr        error
	creates, gets int
}

func (s *payoutConnectorStub) CreatePayout(context.Context, core.RouteDecision, core.Payout, string) (core.ProviderPayout, error) {
	s.creates++
	return s.create, s.createErr
}
func (s *payoutConnectorStub) GetPayout(context.Context, core.RouteDecision, core.Payout) (core.ProviderPayout, error) {
	s.gets++
	return s.get, s.getErr
}
func (*payoutConnectorStub) CancelPayout(context.Context, core.RouteDecision, core.Payout, string) (core.ProviderPayout, error) {
	return core.ProviderPayout{}, core.ErrUnsupported
}

type payoutLedgerStub struct {
	debitErr, creditErr error
	debits, credits     int
}

func (s *payoutLedgerStub) DebitPayout(context.Context, string, string, string, string) error {
	s.debits++
	return s.debitErr
}
func (s *payoutLedgerStub) CreditFailedPayout(context.Context, string, string, string, string) error {
	s.credits++
	return s.creditErr
}
func testPayout(status string) core.Payout {
	return core.Payout{PayoutID: "payout-1", AccountID: "account-1", MerchantID: "merchant-1", Source: core.Money{Amount: "10.00", Currency: "USDT"}, Destination: core.PayoutDestination{Country: "VE", Currency: "VES"}, OperationalStatus: status}
}

func TestPayoutDebitsBeforeProvider(t *testing.T) {
	store := &payoutStoreStub{}
	ledger := &payoutLedgerStub{}
	connector := &payoutConnectorStub{}
	svc := NewPayouts(store, nil, connector, ledger, nil)
	svc.process(context.Background(), testPayout("pending_debit"))
	if ledger.debits != 1 || connector.creates != 0 || len(store.transitions) != 1 || store.transitions[0].next != "pending_provider" {
		t.Fatalf("debits=%d creates=%d transitions=%#v", ledger.debits, connector.creates, store.transitions)
	}
}
func TestPayoutInsufficientBalanceNeverCallsProvider(t *testing.T) {
	store := &payoutStoreStub{}
	ledger := &payoutLedgerStub{debitErr: core.ErrInsufficientBalance}
	connector := &payoutConnectorStub{}
	svc := NewPayouts(store, nil, connector, ledger, nil)
	svc.process(context.Background(), testPayout("pending_debit"))
	if connector.creates != 0 || len(store.transitions) != 1 || store.transitions[0].next != "failed" {
		t.Fatalf("creates=%d transitions=%#v", connector.creates, store.transitions)
	}
}
func TestPayoutProviderRejectionCompensates(t *testing.T) {
	store := &payoutStoreStub{}
	ledger := &payoutLedgerStub{}
	connector := &payoutConnectorStub{create: core.ProviderPayout{ProviderPayoutID: "provider-1", Status: "rejected"}}
	svc := NewPayouts(store, nil, connector, ledger, nil)
	svc.process(context.Background(), testPayout("pending_provider"))
	if len(store.transitions) != 1 || store.transitions[0].next != "pending_compensation" {
		t.Fatalf("transitions=%#v", store.transitions)
	}
	svc.process(context.Background(), testPayout("pending_compensation"))
	if ledger.credits != 1 || store.transitions[1].next != "failed" {
		t.Fatalf("credits=%d transitions=%#v", ledger.credits, store.transitions)
	}
}
func TestPayoutAmbiguousCreateQueriesBeforeRetry(t *testing.T) {
	store := &payoutStoreStub{}
	ledger := &payoutLedgerStub{}
	connector := &payoutConnectorStub{createErr: errors.New("timeout"), get: core.ProviderPayout{ProviderPayoutID: "provider-1", Status: "confirmed"}}
	svc := NewPayouts(store, nil, connector, ledger, nil)
	svc.process(context.Background(), testPayout("pending_provider"))
	if connector.creates != 1 || connector.gets != 1 || len(store.transitions) != 1 || store.transitions[0].next != "confirmed" {
		t.Fatalf("creates=%d gets=%d transitions=%#v", connector.creates, connector.gets, store.transitions)
	}
}

func TestPayoutAmbiguousWithoutLookupNeverResubmits(t *testing.T) {
	store := &payoutStoreStub{}
	ledger := &payoutLedgerStub{}
	connector := &payoutConnectorStub{createErr: errors.New("timeout"), getErr: core.ErrNotFound}
	svc := NewPayouts(store, nil, connector, ledger, nil)
	svc.process(context.Background(), testPayout("pending_provider"))
	if len(store.transitions) != 1 || store.transitions[0].next != "provider_unknown" || connector.creates != 1 {
		t.Fatalf("creates=%d transitions=%#v", connector.creates, store.transitions)
	}
	svc.process(context.Background(), testPayout("provider_unknown"))
	if connector.creates != 1 || connector.gets != 2 {
		t.Fatalf("ambiguous payout was resubmitted: creates=%d gets=%d", connector.creates, connector.gets)
	}
}
