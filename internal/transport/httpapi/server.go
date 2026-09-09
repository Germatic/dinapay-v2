package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/app"
	"github.com/Germatic/dinapay-v2/internal/core"
)

type Server struct {
	payments *app.Payments
	auth     core.Authenticator
}

func New(payments *app.Payments, auth core.Authenticator) http.Handler {
	s := &Server{payments: payments, auth: auth}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "up"}) })
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ready"}) })
	mux.HandleFunc("POST /v2/payments", s.create)
	mux.HandleFunc("GET /v2/payments/{transactionId}", s.get)
	return mux
}

type createRequest struct {
	MerchantID     string         `json:"merchantId"`
	ExternalID     string         `json:"externalId"`
	Amount         string         `json:"amount"`
	Currency       string         `json:"currency"`
	PaymentMethod  string         `json:"paymentMethod"`
	Description    string         `json:"description"`
	Customer       core.Customer  `json:"customer"`
	SuccessURL     string         `json:"successUrl"`
	CancelURL      string         `json:"cancelUrl"`
	ExpirationDate time.Time      `json:"expirationDate"`
	Metadata       map[string]any `json:"metadata"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
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
		Currency: req.Currency, PaymentMethod: req.PaymentMethod, Description: req.Description,
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
	p, ok := s.principal(w, r)
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

func (s *Server) principal(w http.ResponseWriter, r *http.Request) (core.Principal, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	p, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		writeError(w, 401, "unauthorized", "invalid credentials")
		return p, false
	}
	return p, true
}

func mapError(w http.ResponseWriter, err error) {
	switch {
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
