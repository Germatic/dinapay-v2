package app

import (
	"context"
	"testing"
	"time"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
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
	confirmed := core.ProviderEvent{EventID: "provider-event-1", EventType: "payment.provider_confirmed", EventVersion: "1", Source: "webhook", ObservedAt: time.Now(), TransactionID: p.TransactionID, Provider: "test", ProviderConnectionID: "connection1", ProviderPaymentID: "provider1", Data: core.ProviderEventData{Status: "confirmed", RawStatus: "PAY_SUCCESS", Payer: core.Payer{"name": "Juan Perez", "externalId": "payer-1", "institution": map[string]any{"type": "wallet", "name": "MP"}}}}
	result, err := events.Handle(context.Background(), confirmed)
	if err != nil || !result.Changed || result.Status != "confirmed" {
		t.Fatalf("confirmed: %#v %v", result, err)
	}
	stored, err := store.Get(context.Background(), "", "merchant1", p.TransactionID)
	if err != nil || stored.Payer["name"] != "Juan Perez" || stored.Payer["externalId"] != "payer-1" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	result, err = events.Handle(context.Background(), confirmed)
	if err != nil || !result.Duplicate {
		t.Fatalf("duplicate: %#v %v", result, err)
	}
	enrichment := confirmed
	enrichment.EventID = "provider-event-enrichment"
	enrichment.Source = "poll"
	enrichment.Data.Payer = core.Payer{"institution": map[string]any{"type": "wallet", "name": "MODO"}}
	result, err = events.Handle(context.Background(), enrichment)
	if err != nil || result.Changed || result.Status != "confirmed" {
		t.Fatalf("enrichment: %#v %v", result, err)
	}
	stored, err = store.Get(context.Background(), "", "merchant1", p.TransactionID)
	institution, _ := stored.Payer["institution"].(map[string]any)
	if err != nil || stored.Payer["name"] != "Juan Perez" || stored.Payer["externalId"] != "payer-1" || institution["name"] != "MODO" {
		t.Fatalf("enriched stored=%#v err=%v", stored, err)
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

func TestProviderFailureResultExposesInternalMetricCode(t *testing.T) {
	store := memory.NewStore()
	auth := static.NewAuth("test-key=account1:merchant1")
	payments := NewPayments(routerStub{}, connectorStub{}, store, auth, "https://checkout.demo.dinaria.com")
	p, _, err := payments.Create(context.Background(), core.Principal{AccountID: "account1", MerchantID: "merchant1"}, core.CreatePayment{ExternalID: "order-failed", Amount: "0.25", Currency: "USDT", PaymentMethod: "crypto_payment", Customer: core.Customer{"type": "individual", "externalId": "customer1", "country": "UY"}}, "failed-event-key")
	if err != nil {
		t.Fatal(err)
	}
	events := NewProviderEvents(store)
	failure := contract.Failure{Code: "provider_unavailable", Category: "processing", Message: "No fue posible procesar la operación temporalmente."}
	event := core.ProviderEvent{EventID: "provider-failed-1", EventType: "payment.provider_failed", EventVersion: "1", Source: "webhook", ObservedAt: time.Now(), TransactionID: p.TransactionID, Provider: "test", ProviderConnectionID: "connection1", ProviderPaymentID: "provider1", Data: core.ProviderEventData{Status: "failed", RawStatus: "ERROR", Failure: &failure}}
	result, err := events.Handle(context.Background(), event)
	if err != nil || result.FailureCode != "provider_unavailable" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	duplicate, err := events.Handle(context.Background(), event)
	if err != nil || !duplicate.Duplicate || duplicate.FailureCode != "" {
		t.Fatalf("duplicate=%#v err=%v", duplicate, err)
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
