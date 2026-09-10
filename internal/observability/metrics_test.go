package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPMetricsUseNormalizedRouteAndStatusClass(t *testing.T) {
	ObserveHTTP("GET", "GET /v2/payments/{transactionId}", 200, 12*time.Millisecond)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `route="GET /v2/payments/{transactionId}"`) || !strings.Contains(body, `status_class="2xx"`) || strings.Contains(body, "transaction-123") {
		t.Fatalf("unexpected metrics: %s", body)
	}
}

func TestPersistentMetricsExposeUnknownPayouts(t *testing.T) {
	SetPersistent(0, 0, 0, 0, 2, 90)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, "dinapay_payout_provider_unknown 2") || !strings.Contains(body, "dinapay_payout_provider_unknown_oldest_seconds 90") {
		t.Fatalf("metrics=%s", body)
	}
}
