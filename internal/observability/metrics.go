package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const service = "dinapay-v2"

var buckets = [...]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

type httpKey struct{ method, route, status string }
type durationKey struct{ method, route string }
type providerFailureKey struct{ operation, provider, code string }
type histogram struct {
	count   uint64
	sum     float64
	buckets [len(buckets)]uint64
}

var metrics = struct {
	sync.RWMutex
	requests  map[httpKey]uint64
	durations map[durationKey]histogram
	gauges    map[string]float64
}{requests: map[httpKey]uint64{}, durations: map[durationKey]histogram{}, gauges: map[string]float64{}}

// Provider-failure counters are kept separately from the HTTP histograms so
// recording a business failure never contends on the HTTP metrics mutex. The
// number of series is bounded by the operation, provider and canonical failure
// catalogs; resource identifiers and raw provider messages are never labels.
var providerFailures struct {
	sync.Mutex
	snapshot atomic.Pointer[map[providerFailureKey]*atomic.Uint64]
}

func ObserveProviderFailure(operation, provider, code string) {
	if operation == "" || provider == "" || code == "" {
		return
	}
	key := providerFailureKey{operation: operation, provider: provider, code: code}
	counter := providerFailureCounter(key)
	counter.Add(1)
}

func providerFailureCounter(key providerFailureKey) *atomic.Uint64 {
	if current := providerFailures.snapshot.Load(); current != nil {
		if counter := (*current)[key]; counter != nil {
			return counter
		}
	}
	providerFailures.Lock()
	defer providerFailures.Unlock()
	current := providerFailures.snapshot.Load()
	if current != nil {
		if counter := (*current)[key]; counter != nil {
			return counter
		}
	}
	next := make(map[providerFailureKey]*atomic.Uint64, 1)
	if current != nil {
		next = make(map[providerFailureKey]*atomic.Uint64, len(*current)+1)
		for existingKey, counter := range *current {
			next[existingKey] = counter
		}
	}
	counter := &atomic.Uint64{}
	next[key] = counter
	providerFailures.snapshot.Store(&next)
	return counter
}

func SetPersistent(webhooks, webhookAge, ledger, ledgerAge, unknownPayouts, unknownPayoutAge, unknownRefunds, unknownRefundAge float64) {
	metrics.Lock()
	defer metrics.Unlock()
	metrics.gauges["dinapay_webhook_outbox_pending"] = webhooks
	metrics.gauges["dinapay_webhook_outbox_oldest_seconds"] = webhookAge
	metrics.gauges["dinapay_ledger_outbox_pending"] = ledger
	metrics.gauges["dinapay_ledger_outbox_oldest_seconds"] = ledgerAge
	metrics.gauges["dinapay_payout_provider_unknown"] = unknownPayouts
	metrics.gauges["dinapay_payout_provider_unknown_oldest_seconds"] = unknownPayoutAge
	metrics.gauges["dinapay_refund_provider_unknown"] = unknownRefunds
	metrics.gauges["dinapay_refund_provider_unknown_oldest_seconds"] = unknownRefundAge
}

func ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	statusClass := strconv.Itoa(status/100) + "xx"
	metrics.Lock()
	defer metrics.Unlock()
	metrics.requests[httpKey{method, route, statusClass}]++
	key := durationKey{method, route}
	h := metrics.durations[key]
	seconds := elapsed.Seconds()
	h.count++
	h.sum += seconds
	for i, limit := range buckets {
		if seconds <= limit {
			h.buckets[i]++
		}
	}
	metrics.durations[key] = h
}

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.RLock()
		defer metrics.RUnlock()
		var lines []string
		for key, value := range metrics.requests {
			lines = append(lines, fmt.Sprintf("dinapay_http_requests_total{service=%q,method=%q,route=%q,status_class=%q} %d", service, key.method, key.route, key.status, value))
		}
		for key, h := range metrics.durations {
			for i, limit := range buckets {
				lines = append(lines, fmt.Sprintf("dinapay_http_request_duration_seconds_bucket{service=%q,method=%q,route=%q,le=%q} %d", service, key.method, key.route, strconv.FormatFloat(limit, 'g', -1, 64), h.buckets[i]))
			}
			lines = append(lines, fmt.Sprintf("dinapay_http_request_duration_seconds_bucket{service=%q,method=%q,route=%q,le=\"+Inf\"} %d", service, key.method, key.route, h.count))
			lines = append(lines, fmt.Sprintf("dinapay_http_request_duration_seconds_sum{service=%q,method=%q,route=%q} %g", service, key.method, key.route, h.sum))
			lines = append(lines, fmt.Sprintf("dinapay_http_request_duration_seconds_count{service=%q,method=%q,route=%q} %d", service, key.method, key.route, h.count))
		}
		for name, value := range metrics.gauges {
			lines = append(lines, fmt.Sprintf("%s %g", name, value))
		}
		if failures := providerFailures.snapshot.Load(); failures != nil {
			for key, counter := range *failures {
				value := counter.Load()
				lines = append(lines, fmt.Sprintf("dinapay_provider_operation_failures_total{service=%q,operation=%q,provider=%q,code=%q} %d", service, key.operation, key.provider, key.code, value))
				if key.code == "unknown_error" {
					lines = append(lines, fmt.Sprintf("dinapay_provider_unmapped_failures_total{service=%q,operation=%q,provider=%q} %d", service, key.operation, key.provider, value))
				}
			}
		}
		sort.Strings(lines)
		_, _ = fmt.Fprintln(w, strings.Join(lines, "\n"))
	})
}
