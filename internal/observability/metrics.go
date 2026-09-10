package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const service = "dinapay-v2"

var buckets = [...]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

type httpKey struct{ method, route, status string }
type durationKey struct{ method, route string }
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

func SetPersistent(webhooks, webhookAge, ledger, ledgerAge float64) {
	metrics.Lock()
	defer metrics.Unlock()
	metrics.gauges["dinapay_webhook_outbox_pending"] = webhooks
	metrics.gauges["dinapay_webhook_outbox_oldest_seconds"] = webhookAge
	metrics.gauges["dinapay_ledger_outbox_pending"] = ledger
	metrics.gauges["dinapay_ledger_outbox_oldest_seconds"] = ledgerAge
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
		sort.Strings(lines)
		_, _ = fmt.Fprintln(w, strings.Join(lines, "\n"))
	})
}
