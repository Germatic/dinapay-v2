package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Germatic/dinapay-v2/internal/adapters/memory"
	"github.com/Germatic/dinapay-v2/internal/adapters/static"
	"github.com/Germatic/dinapay-v2/internal/app"
	"github.com/Germatic/dinapay-v2/internal/core"
)

func TestHostedCheckoutRendersSafeQRAndMinimalStatus(t *testing.T) {
	const transactionID = "8dd5d1ee-1ba0-4311-bbbd-fd19765b2f93"
	store := memory.NewStore()
	_, _, _ = store.BeginCreate(context.Background(), "merchant-1", "checkout-test", "hash", transactionID)
	payment := core.Payment{
		TransactionID: transactionID, AccountID: "account-1", MerchantID: "merchant-1", ExternalID: "private-order",
		Status: "started", Amount: "150.00", Currency: "ARS", PaymentMethod: "qr", CreationDate: time.Now().UTC(),
		ExpirationDate: time.Now().UTC().Add(15 * time.Minute), Customer: core.Customer{"email": "private@example.com"}, Version: 1,
		PaymentData:       map[string]any{"type": "qr", "qr": map[string]any{"imageBase64": "iVBORw0KGgo="}},
		ProviderPaymentID: "private-provider-id",
	}
	if err := store.CompleteCreate(context.Background(), payment, core.MerchantEvent{EventID: "event-1"}, "checkout-test"); err != nil {
		t.Fatal(err)
	}
	payments := app.NewPayments(nil, nil, store, static.NewAuth("key=account-1:merchant-1"), "https://checkout.example")
	handler := New(payments, nil, nil, nil, static.NewAuth("key=account-1:merchant-1"), "service-token")

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/pay/"+transactionID, nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Pagá con QR") || !strings.Contains(page.Body.String(), "data:image/png;base64,") {
		t.Fatalf("status=%d body=%s", page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), "private@example.com") || strings.Contains(page.Body.String(), "private-provider-id") || page.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("checkout leaked private data or omitted CSP")
	}
	publicPage := httptest.NewRecorder()
	handler.ServeHTTP(publicPage, httptest.NewRequest(http.MethodGet, "/v2/pay/"+transactionID, nil))
	publicBody := strings.ReplaceAll(publicPage.Body.String(), `\/`, "/")
	if publicPage.Code != http.StatusOK || !strings.Contains(publicBody, "/v2/public/v1/checkout/payments/"+transactionID+"/status") {
		t.Fatalf("prefixed checkout status=%d body=%s", publicPage.Code, publicPage.Body.String())
	}

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/public/v1/checkout/payments/"+transactionID+"/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"status":"started"`) || strings.Contains(status.Body.String(), "paymentData") {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	etag := status.Header().Get("ETag")
	conditional := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/public/v1/checkout/payments/"+transactionID+"/status", nil)
	req.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(conditional, req)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional status=%d body=%s", conditional.Code, conditional.Body.String())
	}
}

func TestMapErrorSanitizesDependencyFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Request-Id", "request-123")

	mapError(recorder, errors.New(`create provider payment: upstream status 503: {"provider":"secret detail"}`))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "dependency_unavailable" || body["message"] != "The requested service is temporarily unavailable." || body["requestId"] != "request-123" {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "provider") || strings.Contains(recorder.Body.String(), "secret detail") {
		t.Fatalf("internal upstream detail leaked: %s", recorder.Body.String())
	}
}

type dashboardReaderStub struct {
	accountID      string
	merchantID     string
	options        core.PaymentListOptions
	payoutOptions  core.PayoutListOptions
	summaryOptions core.DashboardSummaryOptions
}

func (s *dashboardReaderStub) ListDashboardPayments(_ context.Context, accountID, merchantID string, options core.PaymentListOptions) (core.PaymentPage, error) {
	s.accountID, s.merchantID, s.options = accountID, merchantID, options
	return core.PaymentPage{Data: []core.Payment{}, HasMore: false}, nil
}

func (s *dashboardReaderStub) ListDashboardPayouts(_ context.Context, accountID, merchantID string, options core.PayoutListOptions) (core.PayoutPage, error) {
	s.accountID, s.merchantID = accountID, merchantID
	s.payoutOptions = options
	return core.PayoutPage{Data: []core.Payout{}}, nil
}

func (s *dashboardReaderStub) DashboardSummary(_ context.Context, accountID string, options core.DashboardSummaryOptions) (core.DashboardSummary, error) {
	s.accountID, s.summaryOptions = accountID, options
	return core.DashboardSummary{Currencies: []core.DashboardCurrencySummary{}}, nil
}

func TestDashboardSummaryForwardsScopeAndFilters(t *testing.T) {
	reader := &dashboardReaderStub{}
	h := NewWithDashboardReader(nil, nil, nil, nil, nil, "service-secret", reader, "dashboard-secret")
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/summary?accountId=account-1&merchantId=merchant-1&direction=in&currency=usd&confirmedAfter=2026-09-01T00:00:00Z", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if reader.accountID != "account-1" || reader.summaryOptions.MerchantID != "merchant-1" || reader.summaryOptions.Direction != "in" || reader.summaryOptions.Currency != "USD" || reader.summaryOptions.ConfirmedAfter == nil {
		t.Fatalf("summary filters not forwarded: %+v", reader)
	}
}

func TestDashboardPaymentReadUsesDedicatedCredentialAndFilters(t *testing.T) {
	reader := &dashboardReaderStub{}
	h := NewWithDashboardReader(nil, nil, nil, nil, nil, "service-secret", reader, "dashboard-secret")

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/payments", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("without dashboard token status=%d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/payments?accountId=account-1&merchantId=merchant-1&limit=25&cursor=next&status=confirmed&currency=USD&externalId=order-1&createdAfter=2026-09-01T00:00:00Z&createdBefore=2026-10-01T00:00:00Z&confirmedAfter=2026-09-02T00:00:00Z&confirmedBefore=2026-10-02T00:00:00Z", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if reader.accountID != "account-1" || reader.merchantID != "merchant-1" || reader.options.Limit != 25 || reader.options.Cursor != "next" {
		t.Fatalf("filters not forwarded: %+v", reader)
	}
	if reader.options.Status != "confirmed" || reader.options.Currency != "USD" || reader.options.ExternalID != "order-1" || reader.options.CreatedAfter == nil || reader.options.CreatedBefore == nil || reader.options.ConfirmedAfter == nil || reader.options.ConfirmedBefore == nil {
		t.Fatalf("extended filters not forwarded: %+v", reader.options)
	}
}

func TestDashboardRowsExposeScopeOnlyOnInternalRead(t *testing.T) {
	reader := &dashboardReaderStub{}
	readerResult := core.PaymentPage{Data: []core.Payment{{TransactionID: "tx-1", AccountID: "account-1", MerchantID: "merchant-1", Status: "confirmed", Amount: "1.00", Currency: "USD"}}}
	readerWithResult := &dashboardResultStub{dashboardReaderStub: *reader, paymentPage: readerResult}
	h := NewWithDashboardReader(nil, nil, nil, nil, nil, "service-secret", readerWithResult, "dashboard-secret")
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/payments", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0]["accountId"] != "account-1" || body.Data[0]["merchantId"] != "merchant-1" {
		t.Fatalf("scope missing from dashboard row: %s", recorder.Body.String())
	}
}

type dashboardResultStub struct {
	dashboardReaderStub
	paymentPage core.PaymentPage
}

type dashboardFailureStub struct {
	dashboardReaderStub
	result core.OperationalFailure
	err    error
}

func (s *dashboardFailureStub) GetDashboardPaymentFailure(_ context.Context, id string) (core.OperationalFailure, error) {
	s.result.ResourceID = id
	s.result.ResourceType = "payment"
	return s.result, s.err
}

func (s *dashboardFailureStub) GetDashboardPayoutFailure(_ context.Context, id string) (core.OperationalFailure, error) {
	s.result.ResourceID = id
	s.result.ResourceType = "payout"
	return s.result, s.err
}

func TestDashboardFailureRequiresDedicatedTokenAndExposesInternalDetail(t *testing.T) {
	reader := &dashboardFailureStub{result: core.OperationalFailure{Status: "failed", Provider: "insular", Failure: map[string]any{"code": "unknown_error"}, ProviderFailure: map[string]any{"code": "627"}}}
	h := NewWithDashboardReader(nil, nil, nil, nil, nil, "service-secret", reader, "dashboard-secret")
	path := "/internal/v1/dashboard/payouts/af820a06-8806-4f62-9eb5-b975afcd26bc/failure"
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Body.String(), `"providerFailure":{"code":"627"}`) {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestDashboardFailureRejectsInvalidResourceID(t *testing.T) {
	reader := &dashboardFailureStub{}
	h := NewWithDashboardReader(nil, nil, nil, nil, nil, "", reader, "dashboard-secret")
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/payments/not-a-uuid/failure", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func (s *dashboardResultStub) ListDashboardPayments(_ context.Context, accountID, merchantID string, options core.PaymentListOptions) (core.PaymentPage, error) {
	s.accountID, s.merchantID, s.options = accountID, merchantID, options
	return s.paymentPage, nil
}

func TestDashboardRejectsInvalidDate(t *testing.T) {
	h := NewWithDashboardReader(nil, nil, nil, nil, nil, "service-secret", &dashboardReaderStub{}, "dashboard-secret")
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/payouts?confirmedAfter=yesterday", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "confirmedAfter") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRequestIDIsGeneratedAndReturnedInErrors(t *testing.T) {
	h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusBadRequest, "invalid_request", "bad request")
	}))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	id := recorder.Header().Get("X-Request-Id")
	if id == "" {
		t.Fatal("missing X-Request-Id response header")
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"requestId":"`+id+`"`) {
		t.Fatalf("response body does not contain request id %q: %s", id, body)
	}
}

func TestValidClientRequestIDIsPreserved(t *testing.T) {
	h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "merchant-request-123")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if got := recorder.Header().Get("X-Request-Id"); got != "merchant-request-123" {
		t.Fatalf("X-Request-Id = %q", got)
	}
}

func TestTraceparentIsGeneratedAndReturned(t *testing.T) {
	h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if core.Traceparent(r.Context()) == "" {
			t.Fatal("traceparent missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test", nil))
	if got := recorder.Header().Get("traceparent"); !validTraceparent(got) {
		t.Fatalf("invalid generated traceparent %q", got)
	}
}
