package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestCreatePaymentForwardsCollectionKey(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transactionId":"tx-1","provider":"transferdirecto","providerConnectionId":"td-sandbox","providerPaymentId":"646180157034181180","status":"pending"}`))
	}))
	defer server.Close()

	client := NewConnectors(map[string]string{"td": server.URL}, "token")
	route := core.RouteDecision{ConnectorID: "td", Provider: "transferdirecto", ProviderConnectionID: "td-sandbox", Rail: "spei", DestinationMode: "reusable"}
	payment := core.Payment{TransactionID: "tx-1", Amount: "10.00", Currency: "MXN", PaymentMethod: "bank_transfer", CollectionKey: "customer-123", Customer: core.Customer{"country": "MX"}}
	if _, err := client.CreatePayment(context.Background(), route, payment, "", ""); err != nil {
		t.Fatal(err)
	}
	if body["collectionKey"] != "customer-123" || body["destinationMode"] != "reusable" {
		t.Fatalf("body=%#v", body)
	}
}

func TestPostJSONDecodesStructuredProviderRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"provider_rejected","message":"rejected","failure":{"code":"invalid_amount","category":"validation","message":"El monto indicado no es válido para esta operación."},"providerFailure":{"code":"E_007","message":"Monto invalido"}}}`))
	}))
	defer server.Close()
	err := postJSON(context.Background(), server.Client(), server.URL, "", "", map[string]any{}, &map[string]any{})
	var rejected *core.ProviderRejectedError
	if !errors.As(err, &rejected) || !errors.Is(err, core.ErrProviderRejected) {
		t.Fatalf("error=%#v", err)
	}
	if rejected.Failure == nil || rejected.Failure.Code != string(contract.PayoutInvalidAmount) || rejected.ProviderFailure == nil || rejected.ProviderFailure.Code != "E_007" {
		t.Fatalf("rejected=%#v", rejected)
	}
}
