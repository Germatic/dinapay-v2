package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type dashboardReaderStub struct {
	accountID  string
	merchantID string
	options    core.PaymentListOptions
}

func (s *dashboardReaderStub) ListDashboardPayments(_ context.Context, accountID, merchantID string, options core.PaymentListOptions) (core.PaymentPage, error) {
	s.accountID, s.merchantID, s.options = accountID, merchantID, options
	return core.PaymentPage{Data: []core.Payment{}, HasMore: false}, nil
}

func (s *dashboardReaderStub) ListDashboardPayouts(_ context.Context, accountID, merchantID string, options core.PayoutListOptions) (core.PayoutPage, error) {
	s.accountID, s.merchantID = accountID, merchantID
	return core.PayoutPage{Data: []core.Payout{}}, nil
}

func TestDashboardPaymentReadUsesDedicatedCredentialAndFilters(t *testing.T) {
	reader := &dashboardReaderStub{}
	h := NewWithDashboardReader(nil, nil, nil, nil, nil, "service-secret", reader, "dashboard-secret")

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/payments", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("without dashboard token status=%d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/dashboard/payments?accountId=account-1&merchantId=merchant-1&limit=25&cursor=next", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if reader.accountID != "account-1" || reader.merchantID != "merchant-1" || reader.options.Limit != 25 || reader.options.Cursor != "next" {
		t.Fatalf("filters not forwarded: %+v", reader)
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
