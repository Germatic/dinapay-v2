package app

import (
	"context"
	"testing"
	"time"

	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/adapters/static"
	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestProviderEventIsIdempotentAndCannotRegress(t *testing.T) {
	store := memory.NewStore()
	auth := static.NewAuth("test-key=account1:merchant1")
	payments := NewPayments(routerStub{}, connectorStub{}, store, auth, "https://checkout.demo.dinaria.com")
	p, _, err := payments.Create(context.Background(), core.Principal{AccountID: "account1", MerchantID: "merchant1"}, core.CreatePayment{ExternalID: "order-event", Amount: "0.25", Currency: "USDT", PaymentMethod: "crypto_payment", Customer: core.Customer{"type": "individual", "externalId": "customer1", "country": "UY"}}, "event-key")
	if err != nil {
		t.Fatal(err)
	}
	events := NewProviderEvents(store)
	confirmed := core.ProviderEvent{EventID: "provider-event-1", EventType: "payment.provider_confirmed", EventVersion: "1", Source: "webhook", ObservedAt: time.Now(), TransactionID: p.TransactionID, Provider: "test", ProviderConnectionID: "connection1", ProviderPaymentID: "provider1", Data: core.ProviderEventData{Status: "confirmed", RawStatus: "PAY_SUCCESS"}}
	result, err := events.Handle(context.Background(), confirmed)
	if err != nil || !result.Changed || result.Status != "confirmed" {
		t.Fatalf("confirmed: %#v %v", result, err)
	}
	result, err = events.Handle(context.Background(), confirmed)
	if err != nil || !result.Duplicate {
		t.Fatalf("duplicate: %#v %v", result, err)
	}
	late := confirmed
	late.EventID = "provider-event-2"
	late.EventType = "payment.provider_pending"
	late.Data.Status = "pending"
	result, err = events.Handle(context.Background(), late)
	if err != nil || result.Changed || result.Status != "confirmed" {
		t.Fatalf("late event: %#v %v", result, err)
	}
}

func TestLateProviderConfirmationOverridesExpiration(t *testing.T) {
	store := memory.NewStore()
	auth := static.NewAuth("test-key=account1:merchant1")
	payments := NewPayments(routerStub{}, connectorStub{}, store, auth, "https://checkout.demo.dinaria.com")
	p, _, err := payments.Create(context.Background(), core.Principal{AccountID: "account1", MerchantID: "merchant1"}, core.CreatePayment{ExternalID: "order-late-confirmation", Amount: "1.00", Currency: "MXN", PaymentMethod: "bank_transfer", Customer: core.Customer{"type": "individual", "country": "MX"}}, "late-confirmation-key")
	if err != nil {
		t.Fatal(err)
	}
	events := NewProviderEvents(store)
	base := core.ProviderEvent{EventVersion: "1", Source: "timer", ObservedAt: time.Now(), TransactionID: p.TransactionID, Provider: "test", ProviderConnectionID: "connection1", ProviderPaymentID: "provider1"}
	expired := base
	expired.EventID = "provider-expired"
	expired.EventType = "payment.provider_expired"
	expired.Data = core.ProviderEventData{Status: "expired", RawStatus: "validity_elapsed"}
	if result, handleErr := events.Handle(context.Background(), expired); handleErr != nil || !result.Changed || result.Status != "expired" {
		t.Fatalf("expired result=%#v err=%v", result, handleErr)
	}
	confirmed := base
	confirmed.EventID = "provider-confirmed-late"
	confirmed.EventType = "payment.provider_confirmed"
	confirmed.Source = "webhook"
	confirmed.Data = core.ProviderEventData{Status: "confirmed", RawStatus: "spei.in.completed"}
	if result, handleErr := events.Handle(context.Background(), confirmed); handleErr != nil || !result.Changed || result.Status != "confirmed" {
		t.Fatalf("confirmed result=%#v err=%v", result, handleErr)
	}
}
