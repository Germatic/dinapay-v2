package app

import (
	"context"
	"testing"
	"time"

	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/adapters/static"
	"github.com/Germatic/dinapay-v2/internal/core"
)

type routerStub struct{}

func (routerStub) Resolve(_ context.Context, r core.RouteRequest) (core.RouteDecision, error) {
	return core.RouteDecision{RouteDecisionID: "route1", RequestID: r.RequestID, TransactionID: r.TransactionID, Status: "selected", ConnectorID: "connector-test", Provider: "test", ProviderConnectionID: "connection1"}, nil
}

type connectorStub struct{}

func (connectorStub) CreatePayment(_ context.Context, _ core.RouteDecision, p core.Payment, _, _ string) (core.ProviderPayment, error) {
	return core.ProviderPayment{TransactionID: p.TransactionID, Provider: "test", ProviderConnectionID: "connection1", ProviderPaymentID: "provider1", Status: "created", ExpiresAt: time.Date(2026, 9, 9, 19, 30, 0, 0, time.UTC), Completion: map[string]any{"type": "redirect", "links": map[string]any{"web": "https://example.com"}}}, nil
}

func TestCreateIsIdempotent(t *testing.T) {
	auth := static.NewAuth("test-key=account1:merchant1")
	svc := NewPayments(routerStub{}, connectorStub{}, memory.NewStore(), auth, "https://checkout.demo.dinaria.com")
	in := core.CreatePayment{ExternalID: "order1", Amount: "0.25", Currency: "USDT", PaymentMethod: "crypto_payment", Customer: core.Customer{"type": "individual", "externalId": "customer1", "country": "UY"}}
	p1, replay1, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, in, "key1")
	if err != nil || replay1 {
		t.Fatalf("first create: replay=%v err=%v", replay1, err)
	}
	p2, replay2, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, in, "key1")
	if err != nil || !replay2 || p1.TransactionID != p2.TransactionID {
		t.Fatalf("replay: p1=%s p2=%s replay=%v err=%v", p1.TransactionID, p2.TransactionID, replay2, err)
	}
	redirect := p1.PaymentData["redirect"].(map[string]any)
	if redirect["recommendedAlternative"] != "web" {
		t.Fatalf("unexpected public paymentData: %#v", p1.PaymentData)
	}
}

func TestLegacyReadUsesStandardCheckoutURL(t *testing.T) {
	store := memory.NewStore()
	// CompleteCreate is used only to seed the in-memory repository for this read test.
	_, _, _ = store.BeginCreate(context.Background(), "merchant1", "legacy", "hash", "legacy-id")
	_ = store.CompleteCreate(context.Background(), core.Payment{TransactionID: "legacy-id", MerchantID: "merchant1", Origin: "v1", ActionURL: "https://provider.example/pay"}, core.MerchantEvent{EventID: "e1"}, "legacy")
	svc := NewPayments(routerStub{}, connectorStub{}, store, static.NewAuth("test-key=account1:merchant1"), "https://checkout.demo.dinaria.com")
	p, err := svc.Get(context.Background(), core.Principal{MerchantID: "merchant1"}, "legacy-id")
	if err != nil {
		t.Fatal(err)
	}
	if p.ActionURL != "https://checkout.demo.dinaria.com/pay/legacy-id" {
		t.Fatalf("actionUrl = %s", p.ActionURL)
	}
}
