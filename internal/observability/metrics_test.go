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

func TestPersistentMetricsExposeUnknownProviderOutcomes(t *testing.T) {
	SetPersistent(0, 0, 0, 0, 2, 90, 1, 45)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, "dinapay_payout_provider_unknown 2") || !strings.Contains(body, "dinapay_payout_provider_unknown_oldest_seconds 90") {
		t.Fatalf("metrics=%s", body)
	}
	if !strings.Contains(body, "dinapay_refund_provider_unknown 1") || !strings.Contains(body, "dinapay_refund_provider_unknown_oldest_seconds 45") {
		t.Fatalf("metrics=%s", body)
	}
}

func TestProviderFailureMetricsUseBoundedBusinessLabels(t *testing.T) {
	ObserveProviderFailure("payout", "insular", "destination_rejected")
	ObserveProviderFailure("payout", "insular", "unknown_error")
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `dinapay_provider_operation_failures_total{service="dinapay-v2",operation="payout",provider="insular",code="destination_rejected"} 1`) {
		t.Fatalf("missing normalized failure metric: %s", body)
	}
	if !strings.Contains(body, `dinapay_provider_unmapped_failures_total{service="dinapay-v2",operation="payout",provider="insular"} 1`) {
		t.Fatalf("missing unmapped failure metric: %s", body)
	}
}

func BenchmarkObserveProviderFailure(b *testing.B) {
	for b.Loop() {
		ObserveProviderFailure("payout", "insular", "destination_rejected")
	}
}
