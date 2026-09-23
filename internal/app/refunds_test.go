package app

import (
	"context"
	"errors"
	"testing"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestCreateRefundValidatesMoneyForV2Payment(t *testing.T) {
	payments := memory.NewStore()
	_, _, _ = payments.BeginCreate(context.Background(), "merchant-1", "payment-key", "hash", "payment-1", "payment-external")
	err := payments.CompleteCreate(context.Background(), core.Payment{
		TransactionID: "payment-1", AccountID: "account-1", MerchantID: "merchant-1", ExternalID: "payment-external",
		Status: "confirmed", Amount: "10.00", Currency: "USD", Origin: "v2",
	}, core.MerchantEvent{EventID: "payment-event"}, "payment-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewRefunds(payments, &refundStoreStub{}, nil, &refundConnectorStub{}, &refundLedgerStub{})
	_, _, err = svc.Create(context.Background(), core.Principal{AccountID: "account-1", MerchantID: "merchant-1"}, "", "payment-1", "refund-key", core.CreateRefund{ExternalID: "refund-1", Amount: "1.001"})
	var validation *core.ValidationError
	if !errors.As(err, &validation) || validation.Field != "amount" {
		t.Fatalf("refund amount error=%#v", err)
	}
}

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
	result := core.Refund{OperationalStatus: next, Route: core.RouteDecision{Provider: "binancepay"}}
	if failure, ok := fields["failure"].(contract.Failure); ok {
		result.Failure = map[string]any{"code": failure.Code, "category": failure.Category, "message": failure.Message}
	}
	return result, nil
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
	var observedOperation, observedProvider, observedCode string
	svc := NewRefunds(nil, store, nil, connector, ledger).WithFailureObserver(func(operation, provider, code string) {
		observedOperation, observedProvider, observedCode = operation, provider, code
	})

	svc.process(context.Background(), testRefund("pending_debit"))

	if ledger.debitCalls != 1 || connector.createCalls != 0 {
		t.Fatalf("debit calls=%d provider calls=%d", ledger.debitCalls, connector.createCalls)
	}
	if len(store.transitions) != 1 || store.transitions[0].expected != "pending_debit" || store.transitions[0].next != "failed" {
		t.Fatalf("unexpected transitions: %#v", store.transitions)
	}
	failure, ok := store.transitions[0].fields["failure"].(contract.Failure)
	if !ok || failure.Code != string(contract.RefundInsufficientFunds) {
		t.Fatalf("failure=%#v", store.transitions[0].fields["failure"])
	}
	if observedOperation != "refund" || observedProvider != "binancepay" || observedCode != string(contract.RefundInsufficientFunds) {
		t.Fatalf("observed=%q/%q/%q", observedOperation, observedProvider, observedCode)
	}
}

func TestRefundProviderRejectionIsCompensated(t *testing.T) {
	store := &refundStoreStub{}
	ledger := &refundLedgerStub{}
	public := contract.NewRefund(contract.RefundRejected)
	native := contract.ProviderFailure{Code: "400612", Message: "native rejection"}
	connector := &refundConnectorStub{createResult: core.ProviderRefund{ProviderRefundID: "provider-refund-1", Status: "rejected", RawStatus: "REFUND_FAIL", Failure: &public, ProviderFailure: &native}}
	svc := NewRefunds(nil, store, nil, connector, ledger)

	svc.process(context.Background(), testRefund("pending_provider"))
	if len(store.transitions) != 1 || store.transitions[0].next != "pending_compensation" {
		t.Fatalf("provider rejection transition: %#v", store.transitions)
	}
	if got := store.transitions[0].fields["failure"].(contract.Failure); got.Code != string(contract.RefundRejected) {
		t.Fatalf("failure=%#v", got)
	}
	if got := store.transitions[0].fields["providerFailure"].(contract.ProviderFailure); got.Code != "400612" {
		t.Fatalf("provider failure=%#v", got)
	}
	svc.process(context.Background(), testRefund("pending_compensation"))
	if ledger.creditCalls != 1 || len(store.transitions) != 2 || store.transitions[1].next != "failed" {
		t.Fatalf("compensation calls=%d transitions=%#v", ledger.creditCalls, store.transitions)
	}
}

func TestRefundProviderRejectionErrorMovesToCompensation(t *testing.T) {
	store := &refundStoreStub{}
	ledger := &refundLedgerStub{}
	public := contract.NewRefund(contract.RefundRejected)
	native := contract.ProviderFailure{Code: "full_refund_required", Message: "PVS only supports full refunds"}
	connector := &refundConnectorStub{createErr: &core.ProviderRejectedError{Message: "provider rejected partial refund", Failure: &public, ProviderFailure: &native}}
	svc := NewRefunds(nil, store, nil, connector, ledger)

	svc.process(context.Background(), testRefund("pending_provider"))

	if connector.createCalls != 1 || connector.getCalls != 0 {
		t.Fatalf("create calls=%d get calls=%d", connector.createCalls, connector.getCalls)
	}
	if len(store.transitions) != 1 || store.transitions[0].next != "pending_compensation" {
		t.Fatalf("transitions=%#v", store.transitions)
	}
	if got := store.transitions[0].fields["failure"].(contract.Failure); got.Code != string(contract.RefundRejected) {
		t.Fatalf("failure=%#v", got)
	}
	if got := store.transitions[0].fields["providerFailure"].(contract.ProviderFailure); got.Code != "full_refund_required" {
		t.Fatalf("provider failure=%#v", got)
	}
}

func TestRefundInvalidConnectorFailureFallsBackWithoutExposure(t *testing.T) {
	invalid := contract.Failure{Code: "binance_native", Category: "provider", Message: "native message"}
	public, native := normalizeRefundFailure(core.ProviderRefund{RawStatus: "REFUND_FAIL", Failure: &invalid})
	if public.Code != string(contract.RefundUnknownError) || !contract.ValidRefund(public) {
		t.Fatalf("public=%#v", public)
	}
	if native.Code != "REFUND_FAIL" || native.Details["invalidNormalizedFailure"] == nil {
		t.Fatalf("native=%#v", native)
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
