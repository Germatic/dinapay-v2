package app

import (
	"context"
	"sync"
	"testing"

	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/adapters/static"
	"github.com/Germatic/dinapay-v2/internal/core"
)

type riskGateStub struct {
	mu    sync.Mutex
	calls []core.RiskControlObservation
	err   error
}

func (s *riskGateStub) EvaluateRisk(_ context.Context, observation core.RiskControlObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, observation)
	return s.err
}

func (s *riskGateStub) snapshot() []core.RiskControlObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.RiskControlObservation(nil), s.calls...)
}

func TestPaymentCreationEvaluatesCustomerControls(t *testing.T) {
	gate := &riskGateStub{}
	service := NewPayments(routerStub{}, connectorStub{}, memory.NewStore(), static.NewAuth("test-key=account1:merchant1"), "").WithRiskControlGate(gate)
	_, _, err := service.Create(context.Background(), core.Principal{AccountID: "account1", MerchantID: "merchant1"}, core.CreatePayment{ExternalID: "risk-payment", Amount: "100.00", Currency: "ARS", PaymentMethod: "qr", Customer: core.Customer{"type": "individual", "firstName": "Juan", "lastName": "Perez", "documentNumber": "20123456717", "country": "AR"}}, "risk-payment-key")
	if err != nil {
		t.Fatal(err)
	}
	assertRiskCalls(t, gate.snapshot(), "payment", map[string]bool{"customer:screening": true, "customer:age_check": true})
}

func TestPayoutCreationEvaluatesRemitterAndRecipientBeforePersist(t *testing.T) {
	gate := &riskGateStub{}
	store := &payoutStoreStub{}
	service := NewPayouts(store, routerStub{}, &payoutConnectorStub{}, nil, static.NewAuth("test-key=account1:merchant1")).WithRiskControlGate(gate)
	in := core.CreatePayout{ExternalID: "risk-payout", Source: core.Money{Amount: "10.00", Currency: "USD"}, Remitter: map[string]any{"type": "individual", "firstName": "Juan", "lastName": "Perez", "documentNumber": "20123456717", "country": "AR"}, Destination: core.PayoutDestination{Country: "VE", Currency: "VES", Beneficiary: map[string]any{"type": "individual", "firstName": "Maria", "lastName": "Gonzalez", "documentNumber": "V40001469", "mobile": "04225786563"}, Rail: map[string]any{"type": "ve_mobile_payment", "bankCode": "0102"}}}
	_, _, err := service.Create(context.Background(), core.Principal{AccountID: "account1", MerchantID: "merchant1"}, "risk-payout-key", in)
	if err != nil {
		t.Fatal(err)
	}
	if store.completes != 1 {
		t.Fatalf("payout persisted %d times", store.completes)
	}
	assertRiskCalls(t, gate.snapshot(), "payout", map[string]bool{"remitter:screening": true, "remitter:age_check": true, "recipient:screening": true, "recipient:age_check": true})
}

func assertRiskCalls(t *testing.T, calls []core.RiskControlObservation, resource string, want map[string]bool) {
	t.Helper()
	got := map[string]bool{}
	for _, call := range calls {
		if call.ResourceType != resource || call.Stage != "creation" {
			t.Fatalf("unexpected call: %#v", call)
		}
		got[call.SubjectRole+":"+call.ControlType] = true
	}
	if len(got) != len(want) {
		t.Fatalf("calls=%#v want=%#v", got, want)
	}
	for key := range want {
		if !got[key] {
			t.Fatalf("missing %s in %#v", key, got)
		}
	}
}
