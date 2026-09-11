package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/app"
	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/Germatic/dinapay-v2/internal/observability"
)

type Server struct {
	payments        *app.Payments
	refunds         *app.Refunds
	payouts         *app.Payouts
	events          *app.ProviderEvents
	auth            core.Authenticator
	serviceToken    string
	dashboardReader core.DashboardPaymentReader
	dashboardToken  string
}

func New(payments *app.Payments, refunds *app.Refunds, payouts *app.Payouts, events *app.ProviderEvents, auth core.Authenticator, serviceToken string) http.Handler {
	return NewWithDashboardReader(payments, refunds, payouts, events, auth, serviceToken, nil, "")
}

func NewWithDashboardReader(payments *app.Payments, refunds *app.Refunds, payouts *app.Payouts, events *app.ProviderEvents, auth core.Authenticator, serviceToken string, dashboardReader core.DashboardPaymentReader, dashboardToken string) http.Handler {
	s := &Server{payments: payments, refunds: refunds, payouts: payouts, events: events, auth: auth, serviceToken: serviceToken, dashboardReader: dashboardReader, dashboardToken: dashboardToken}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "up"}) })
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ready"}) })
	mux.Handle("GET /metrics", internalOnly(observability.Handler()))
	mux.HandleFunc("POST /v2/payments", s.create)
	mux.HandleFunc("GET /v2/payments", s.list)
	mux.HandleFunc("GET /v2/payments/{transactionId}", s.get)
	mux.HandleFunc("POST /v2/payments/{transactionId}/refunds", s.createRefund)
	mux.HandleFunc("GET /v2/payments/{transactionId}/refunds", s.listRefunds)
	mux.HandleFunc("GET /v2/refunds/{refundId}", s.getRefund)
	mux.HandleFunc("POST /v2/payouts", s.createPayout)
	mux.HandleFunc("GET /v2/payouts", s.listPayouts)
	mux.HandleFunc("GET /v2/payouts/{payoutId}", s.getPayout)
	mux.HandleFunc("POST /internal/v1/provider-events", s.providerEvent)
	mux.HandleFunc("GET /internal/v1/dashboard/payments", s.listDashboardPayments)
	mux.HandleFunc("GET /internal/v1/dashboard/payouts", s.listDashboardPayouts)
	return withRequestID(mux)
}

func (s *Server) listDashboardPayouts(w http.ResponseWriter, r *http.Request) {
	if !s.validDashboardToken(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid dashboard read credentials")
		return
	}
	limit, ok := dashboardLimit(w, r)
	if !ok {
		return
	}
	dates, ok := dashboardDates(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	result, err := s.dashboardReader.ListDashboardPayouts(r.Context(), q.Get("accountId"), q.Get("merchantId"), core.PayoutListOptions{Limit: limit, Cursor: q.Get("cursor"), Status: q.Get("status"), Currency: q.Get("currency"), ExternalID: q.Get("externalId"), CreatedAfter: dates[0], CreatedBefore: dates[1], ConfirmedAfter: dates[2], ConfirmedBefore: dates[3]})
	if err != nil {
		slog.Error("dashboard consolidated payout read failed", "error", err, "request_id", core.RequestID(r.Context()))
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dashboardPayoutPage(result))
}

func (s *Server) validDashboardToken(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return s.dashboardReader != nil && s.dashboardToken != "" && len(token) == len(s.dashboardToken) && subtle.ConstantTimeCompare([]byte(token), []byte(s.dashboardToken)) == 1
}

func dashboardLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, 400, "invalid_request", "limit must be between 1 and 100")
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func dashboardDates(w http.ResponseWriter, r *http.Request) ([4]*time.Time, bool) {
	var result [4]*time.Time
	for i, name := range []string{"createdAfter", "createdBefore", "confirmedAfter", "confirmedBefore"} {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", name+" must be an RFC3339 timestamp")
			return result, false
		}
		result[i] = &value
	}
	return result, true
}

func (s *Server) listDashboardPayments(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.dashboardReader == nil || s.dashboardToken == "" || len(token) != len(s.dashboardToken) || subtle.ConstantTimeCompare([]byte(token), []byte(s.dashboardToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid dashboard read credentials")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	dates, ok := dashboardDates(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	result, err := s.dashboardReader.ListDashboardPayments(r.Context(), q.Get("accountId"), q.Get("merchantId"), core.PaymentListOptions{Limit: limit, Cursor: q.Get("cursor"), Status: q.Get("status"), Currency: q.Get("currency"), ExternalID: q.Get("externalId"), CreatedAfter: dates[0], CreatedBefore: dates[1], ConfirmedAfter: dates[2], ConfirmedBefore: dates[3]})
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dashboardPaymentPage(result))
}

func dashboardPaymentPage(page core.PaymentPage) map[string]any {
	data := make([]map[string]any, 0, len(page.Data))
	for _, payment := range page.Data {
		item := map[string]any{}
		encoded, _ := json.Marshal(payment)
		_ = json.Unmarshal(encoded, &item)
		item["accountId"] = payment.AccountID
		item["merchantId"] = payment.MerchantID
		data = append(data, item)
	}
	response := map[string]any{"data": data, "hasMore": page.HasMore}
	if page.NextCursor != "" {
		response["nextCursor"] = page.NextCursor
	}
	return response
}

func dashboardPayoutPage(page core.PayoutPage) map[string]any {
	data := make([]map[string]any, 0, len(page.Data))
	for _, payout := range page.Data {
		item := map[string]any{}
		encoded, _ := json.Marshal(payout)
		_ = json.Unmarshal(encoded, &item)
		item["accountId"] = payout.AccountID
		item["merchantId"] = payout.MerchantID
		data = append(data, item)
	}
	response := map[string]any{"data": data, "hasMore": page.HasMore}
	if page.NextCursor != "" {
		response["nextCursor"] = page.NextCursor
	}
	return response
}

func (s *Server) createPayout(w http.ResponseWriter, r *http.Request) {
	if s.payouts == nil {
		writeError(w, 422, "payout_not_supported", "payouts are not configured")
		return
	}
	p, ok := s.principal(w, r, "payouts:write")
	if !ok {
		return
	}
	var in core.CreatePayout
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	result, replayed, err := s.payouts.Create(r.Context(), p, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		mapError(w, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, 200, result)
		return
	}
	writeJSON(w, 201, result)
}
func (s *Server) getPayout(w http.ResponseWriter, r *http.Request) {
	if s.payouts == nil {
		writeError(w, 422, "payout_not_supported", "payouts are not configured")
		return
	}
	p, ok := s.principal(w, r, "payouts:read")
	if !ok {
		return
	}
	result, err := s.payouts.Get(r.Context(), p, r.PathValue("payoutId"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) listPayouts(w http.ResponseWriter, r *http.Request) {
	if s.payouts == nil {
		writeError(w, 422, "payout_not_supported", "payouts are not configured")
		return
	}
	p, ok := s.principal(w, r, "payouts:read")
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, 400, "invalid_request", "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	result, err := s.payouts.List(r.Context(), p, core.PayoutListOptions{Limit: limit, Cursor: r.URL.Query().Get("cursor"), Status: r.URL.Query().Get("status"), ExternalID: r.URL.Query().Get("externalId")})
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func internalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			var raw [16]byte
			if _, err := rand.Read(raw[:]); err != nil {
				id = fmt.Sprintf("req-%d", time.Now().UnixNano())
			} else {
				id = fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
			}
		}
		traceparent := r.Header.Get("traceparent")
		if !validTraceparent(traceparent) {
			traceparent = newTraceparent()
		}
		w.Header().Set("X-Request-Id", id)
		w.Header().Set("traceparent", traceparent)
		capture := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		ctx := core.WithObservability(r.Context(), id, traceparent)
		request := r.WithContext(ctx)
		next.ServeHTTP(capture, request)
		observability.ObserveHTTP(r.Method, request.Pattern, capture.status, time.Since(started))
		if r.URL.Path != "/health" && r.URL.Path != "/ready" && r.URL.Path != "/metrics" {
			slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", capture.status, "duration_ms", time.Since(started).Milliseconds(), "request_id", id, "traceparent", traceparent)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func validTraceparent(value string) bool {
	if len(value) != 55 || value[2] != '-' || value[35] != '-' || value[52] != '-' {
		return false
	}
	for i, char := range value {
		if i == 2 || i == 35 || i == 52 {
			continue
		}
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return value[3:35] != strings.Repeat("0", 32) && value[36:52] != strings.Repeat("0", 16)
}

func newTraceparent() string {
	var traceID [16]byte
	var spanID [8]byte
	if _, err := rand.Read(traceID[:]); err != nil {
		return ""
	}
	if _, err := rand.Read(spanID[:]); err != nil {
		return ""
	}
	return fmt.Sprintf("00-%x-%x-01", traceID, spanID)
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func (s *Server) createRefund(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "refunds:write")
	if !ok {
		return
	}
	var in core.CreateRefund
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	result, replayed, err := s.refunds.Create(r.Context(), p, bearer(r), r.PathValue("transactionId"), r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		mapError(w, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, 200, result)
		return
	}
	writeJSON(w, 201, result)
}
func (s *Server) listRefunds(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "refunds:read")
	if !ok {
		return
	}
	result, err := s.refunds.List(r.Context(), p, bearer(r), r.PathValue("transactionId"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) getRefund(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "refunds:read")
	if !ok {
		return
	}
	result, err := s.refunds.Get(r.Context(), p, bearer(r), r.PathValue("refundId"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "payments:read")
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, 400, "invalid_request", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	result, err := s.payments.List(r.Context(), p, core.PaymentListOptions{Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) providerEvent(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.serviceToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.serviceToken)) != 1 {
		writeError(w, 401, "unauthorized", "invalid service credentials")
		return
	}
	var event core.ProviderEvent
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&event); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	result, err := s.events.Handle(r.Context(), event)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 202, result)
}

type createRequest struct {
	MerchantID      string         `json:"merchantId"`
	ExternalID      string         `json:"externalId"`
	Amount          string         `json:"amount"`
	Currency        string         `json:"currency"`
	PaymentMethod   string         `json:"paymentMethod"`
	Rail            string         `json:"rail"`
	DestinationMode string         `json:"destinationMode"`
	Description     string         `json:"description"`
	Customer        core.Customer  `json:"customer"`
	SuccessURL      string         `json:"successUrl"`
	CancelURL       string         `json:"cancelUrl"`
	ExpirationDate  time.Time      `json:"expirationDate"`
	Metadata        map[string]any `json:"metadata"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "payments:write")
	if !ok {
		return
	}
	var req createRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	result, replayed, err := s.payments.Create(r.Context(), p, core.CreatePayment{
		MerchantID: req.MerchantID, AccountID: p.AccountID, ExternalID: req.ExternalID, Amount: req.Amount,
		Currency: req.Currency, PaymentMethod: req.PaymentMethod, Rail: req.Rail, DestinationMode: req.DestinationMode, Description: req.Description,
		Customer: req.Customer, SuccessURL: req.SuccessURL, CancelURL: req.CancelURL,
		ExpirationDate: req.ExpirationDate, Metadata: req.Metadata,
	}, r.Header.Get("Idempotency-Key"))
	if err != nil {
		mapError(w, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, 201, result)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "payments:read")
	if !ok {
		return
	}
	result, err := s.payments.Get(r.Context(), p, r.PathValue("transactionId"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) principal(w http.ResponseWriter, r *http.Request, requiredScope string) (core.Principal, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	p, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		writeError(w, 401, "unauthorized", "invalid credentials")
		return p, false
	}
	if len(p.Scopes) > 0 && !slices.Contains(p.Scopes, requiredScope) {
		writeError(w, 403, "forbidden", "API key does not grant the required scope")
		return p, false
	}
	return p, true
}

func mapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrInvalid):
		writeError(w, 400, "invalid_request", err.Error())
	case errors.Is(err, app.ErrInvalid):
		writeError(w, 400, "invalid_request", err.Error())
	case errors.Is(err, app.ErrUnauthorized):
		writeError(w, 403, "forbidden", err.Error())
	case errors.Is(err, app.ErrNotFound):
		writeError(w, 404, "not_found", err.Error())
	case errors.Is(err, app.ErrConflict):
		writeError(w, 409, "idempotency_conflict", err.Error())
	case errors.Is(err, app.ErrInProgress):
		writeError(w, 409, "operation_in_progress", err.Error())
	case errors.Is(err, app.ErrUnsupported):
		writeError(w, 422, "refund_not_supported", err.Error())
	default:
		writeError(w, 503, "dependency_unavailable", err.Error())
	}
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"code": code, "message": message, "requestId": w.Header().Get("X-Request-Id")})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
