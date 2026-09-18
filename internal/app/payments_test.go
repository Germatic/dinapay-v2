package app

import (
	"context"
	"errors"
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

func TestCreateValidatesAndPersistsReturnURLs(t *testing.T) {
	auth := static.NewAuth("test-key=account1:merchant1")
	store := memory.NewStore()
	svc := NewPayments(routerStub{}, connectorStub{}, store, auth, "https://checkout.demo.dinaria.com")
	in := core.CreatePayment{
		ExternalID: "order-return", Amount: "1.00", Currency: "ARS", PaymentMethod: "qr",
		Customer: core.Customer{"country": "AR"}, SuccessURL: "https://merchant.example/success?order=1", CancelURL: "https://merchant.example/cancel",
	}
	payment, _, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, in, "return-key")
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := svc.Checkout(context.Background(), payment.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if checkout.SuccessURL != in.SuccessURL || checkout.CancelURL != in.CancelURL {
		t.Fatalf("return URLs were not persisted: %#v", checkout)
	}

	in.ExternalID = "unsafe-return"
	in.SuccessURL = "javascript:alert(1)"
	if _, _, err = svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, in, "unsafe-return-key"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe return URL error = %v", err)
	}
}

type connectorStub struct{}

func (connectorStub) CreateRefund(context.Context, core.RouteDecision, core.Payment, core.Refund, string) (core.ProviderRefund, error) {
	return core.ProviderRefund{}, core.ErrUnsupported
}
func (connectorStub) GetRefund(context.Context, core.RouteDecision, core.Refund) (core.ProviderRefund, error) {
	return core.ProviderRefund{}, core.ErrUnsupported
}

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

func TestCreateValidatesReusableCollectionKey(t *testing.T) {
	svc := NewPayments(routerStub{}, connectorStub{}, memory.NewStore(), static.NewAuth("test-key=account1:merchant1"), "https://checkout.demo.dinaria.com")
	base := core.CreatePayment{ExternalID: "reusable-1", Amount: "10.00", Currency: "MXN", PaymentMethod: "bank_transfer", DestinationMode: "reusable", Customer: core.Customer{"country": "MX"}}
	if _, _, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, base, "missing-key"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing collectionKey error=%v", err)
	}
	base.CollectionKey = "customer with spaces"
	if _, _, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, base, "invalid-key"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid collectionKey error=%v", err)
	}
	base.CollectionKey = "customer-123"
	if _, _, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, base, "valid-key"); err != nil {
		t.Fatalf("valid collectionKey error=%v", err)
	}
}

func TestCreateUsesProviderRedirectWhenCheckoutIsNotConfigured(t *testing.T) {
	auth := static.NewAuth("test-key=account1:merchant1")
	svc := NewPayments(routerStub{}, connectorStub{}, memory.NewStore(), auth, "")
	in := core.CreatePayment{ExternalID: "order-provider-url", Amount: "0.25", Currency: "USDT", PaymentMethod: "crypto_payment", Customer: core.Customer{"country": "UY"}}
	p, _, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, in, "provider-url-key")
	if err != nil {
		t.Fatal(err)
	}
	if p.ActionURL != "https://example.com" {
		t.Fatalf("actionUrl=%s", p.ActionURL)
	}
}

func TestCreateUsesConfiguredDinariaCheckout(t *testing.T) {
	auth := static.NewAuth("test-key=account1:merchant1")
	svc := NewPayments(routerStub{}, connectorStub{}, memory.NewStore(), auth, "https://checkout.demo.dinaria.com/")
	in := core.CreatePayment{ExternalID: "order-checkout-url", Amount: "0.25", Currency: "USDT", PaymentMethod: "crypto_payment", Customer: core.Customer{"country": "UY"}}
	p, _, err := svc.Create(context.Background(), core.Principal{MerchantID: "merchant1"}, in, "checkout-url-key")
	if err != nil {
		t.Fatal(err)
	}
	if p.ActionURL != "https://checkout.demo.dinaria.com/pay/"+p.TransactionID {
		t.Fatalf("actionUrl=%s", p.ActionURL)
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
