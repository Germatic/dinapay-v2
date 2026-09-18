package httpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
