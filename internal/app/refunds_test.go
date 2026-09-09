package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type refundStoreStub struct {
	transitions []refundTransition
}

type refundTransition struct {
	expected string
	next     string
	fields   map[string]any
}

func (s *refundStoreStub) CreateRefund(context.Context, core.Principal, string, string, core.CreateRefund) (core.Refund, bool, error) {
	return core.Refund{}, false, core.ErrUnsupported
}
func (s *refundStoreStub) GetRefund(context.Context, core.Principal, string) (core.Refund, error) {
	return core.Refund{}, core.ErrNotFound
}
func (s *refundStoreStub) ListRefunds(context.Context, core.Principal, string) (core.RefundList, error) {
	return core.RefundList{}, nil
}
func (s *refundStoreStub) ClaimRefunds(context.Context, int) ([]core.Refund, error) { return nil, nil }
func (s *refundStoreStub) TransitionRefund(_ context.Context, _ string, next string, fields map[string]any) (core.Refund, error) {
	copyFields := make(map[string]any, len(fields))
	for k, v := range fields {
		copyFields[k] = v
	}
	s.transitions = append(s.transitions, refundTransition{expected: fields["expectedStatus"].(string), next: next, fields: copyFields})
	return core.Refund{OperationalStatus: next}, nil
}

type refundLedgerStub struct {
	debitErr    error
	creditErr   error
	debitCalls  int
	creditCalls int
}

func (*refundLedgerStub) CreditConfirmedPayment(context.Context, core.Payment, string) error {
	return nil
}
func (s *refundLedgerStub) DebitConfirmedRefund(context.Context, string, string, string, string) error {
	s.debitCalls++
	return s.debitErr
}
func (s *refundLedgerStub) CreditFailedRefund(context.Context, string, string, string, string) error {
	s.creditCalls++
	return s.creditErr
}

type refundConnectorStub struct {
	createResult core.ProviderRefund
	createErr    error
	getResult    core.ProviderRefund
	getErr       error
	createCalls  int
	getCalls     int
}

func (*refundConnectorStub) CreatePayment(context.Context, core.RouteDecision, core.Payment, string, string) (core.ProviderPayment, error) {
	return core.ProviderPayment{}, core.ErrUnsupported
}
func (s *refundConnectorStub) CreateRefund(context.Context, core.RouteDecision, core.Payment, core.Refund, string) (core.ProviderRefund, error) {
	s.createCalls++
	return s.createResult, s.createErr
}
func (s *refundConnectorStub) GetRefund(context.Context, core.RouteDecision, core.Refund) (core.ProviderRefund, error) {
	s.getCalls++
	return s.getResult, s.getErr
}

func testRefund(status string) core.Refund {
	return core.Refund{
		RefundID: "refund-1", TransactionID: "payment-1", AccountID: "account-1",
		MerchantID: "merchant-1", Amount: "0.10", Currency: "USDT",
		ProviderPaymentID: "provider-payment-1", OperationalStatus: status,
	}
}

func TestRefundInsufficientBalanceFailsBeforeProvider(t *testing.T) {
	store := &refundStoreStub{}
	ledger := &refundLedgerStub{debitErr: core.ErrInsufficientBalance}
	connector := &refundConnectorStub{}
	svc := NewRefunds(nil, store, nil, connector, ledger)

	svc.process(context.Background(), testRefund("pending_debit"))

	if ledger.debitCalls != 1 || connector.createCalls != 0 {
		t.Fatalf("debit calls=%d provider calls=%d", ledger.debitCalls, connector.createCalls)
	}
	if len(store.transitions) != 1 || store.transitions[0].expected != "pending_debit" || store.transitions[0].next != "failed" {
		t.Fatalf("unexpected transitions: %#v", store.transitions)
	}
}

func TestRefundProviderRejectionIsCompensated(t *testing.T) {
	store := &refundStoreStub{}
	ledger := &refundLedgerStub{}
	connector := &refundConnectorStub{createResult: core.ProviderRefund{ProviderRefundID: "provider-refund-1", Status: "rejected", RawStatus: "REJECTED"}}
	svc := NewRefunds(nil, store, nil, connector, ledger)

	svc.process(context.Background(), testRefund("pending_provider"))
	if len(store.transitions) != 1 || store.transitions[0].next != "pending_compensation" {
		t.Fatalf("provider rejection transition: %#v", store.transitions)
	}
	svc.process(context.Background(), testRefund("pending_compensation"))
	if ledger.creditCalls != 1 || len(store.transitions) != 2 || store.transitions[1].next != "failed" {
		t.Fatalf("compensation calls=%d transitions=%#v", ledger.creditCalls, store.transitions)
	}
}

func TestRefundAmbiguousCreateQueriesProviderBeforeRetry(t *testing.T) {
	store := &refundStoreStub{}
	ledger := &refundLedgerStub{}
	connector := &refundConnectorStub{
		createErr: errors.New("provider timeout"),
		getResult: core.ProviderRefund{ProviderRefundID: "provider-refund-1", Status: "confirmed", RawStatus: "REFUNDED"},
	}
	svc := NewRefunds(nil, store, nil, connector, ledger)

	svc.process(context.Background(), testRefund("pending_provider"))

	if connector.createCalls != 1 || connector.getCalls != 1 {
		t.Fatalf("create calls=%d query calls=%d", connector.createCalls, connector.getCalls)
	}
	if len(store.transitions) != 1 || store.transitions[0].next != "succeeded" {
		t.Fatalf("unexpected transitions: %#v", store.transitions)
	}
}

func TestRefundCompensationRetriesWithoutMarkingFailed(t *testing.T) {
	store := &refundStoreStub{}
	ledger := &refundLedgerStub{creditErr: errors.New("ledger unavailable")}
	svc := NewRefunds(nil, store, nil, &refundConnectorStub{}, ledger)

	svc.process(context.Background(), testRefund("pending_compensation"))

	if ledger.creditCalls != 1 || len(store.transitions) != 0 {
		t.Fatalf("credit calls=%d transitions=%#v", ledger.creditCalls, store.transitions)
	}
}
