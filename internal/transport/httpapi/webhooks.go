package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Germatic/dinapay-v2/internal/core"
)

var supportedWebhookEvents = []string{
	"payment.created", "payment.status_changed",
	"refund.created", "refund.status_changed",
	"payout.created", "payout.status_changed",
}

type webhookCreateRequest struct {
	MerchantID string   `json:"merchantId,omitempty"`
	URL        string   `json:"url"`
	EventTypes []string `json:"eventTypes"`
}

type webhookPatchRequest struct {
	URL        *string   `json:"url"`
	EventTypes *[]string `json:"eventTypes"`
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.webhookPrincipal(w, r, true)
	if !ok {
		return
	}
	var in webhookCreateRequest
	if !decodeWebhookRequest(w, r, &in) || !validateWebhookInput(w, in.URL, in.EventTypes) {
		return
	}
	p, ok = s.selectWebhookMerchant(w, r, p, in.MerchantID)
	if !ok {
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		mapError(w, err)
		return
	}
	result, err := s.webhooks.CreateWebhook(r.Context(), p, in.URL, secret, in.EventTypes)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	p, ok := s.webhookPrincipal(w, r, false)
	if !ok {
		return
	}
	result, err := s.webhooks.ListWebhooks(r.Context(), p)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.webhookPrincipal(w, r, true)
	if !ok {
		return
	}
	var in webhookPatchRequest
	if !decodeWebhookRequest(w, r, &in) {
		return
	}
	if in.URL == nil && in.EventTypes == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "At least one of url or eventTypes is required.")
		return
	}
	if in.URL != nil && !validWebhookURL(*in.URL) {
		writeError(w, http.StatusBadRequest, "invalid_webhook_url", "The webhook URL must be a valid HTTP or HTTPS URL.")
		return
	}
	if in.EventTypes != nil && !validateWebhookEvents(w, *in.EventTypes) {
		return
	}
	result, err := s.webhooks.UpdateWebhook(r.Context(), p, r.PathValue("webhookId"), in.URL, in.EventTypes)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.webhookPrincipal(w, r, true)
	if !ok {
		return
	}
	if err := s.webhooks.DeleteWebhook(r.Context(), p, r.PathValue("webhookId")); err != nil {
		mapError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	p, ok := s.webhookPrincipal(w, r, true)
	if !ok {
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		mapError(w, err)
		return
	}
	result, err := s.webhooks.RotateWebhookSecret(r.Context(), p, r.PathValue("webhookId"), secret)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) dashboardWebhookPrincipal(w http.ResponseWriter, r *http.Request) (core.Principal, bool) {
	if !s.validDashboardToken(r) || s.webhooks == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid dashboard credentials")
		return core.Principal{}, false
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	merchantID := strings.TrimSpace(r.URL.Query().Get("merchantId"))
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "accountId is required")
		return core.Principal{}, false
	}
	if merchantID != "" {
		authorizer, ok := s.webhooks.(core.WebhookScopeStore)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "webhook scope validation is unavailable")
			return core.Principal{}, false
		}
		owned, err := authorizer.OwnsWebhookScope(r.Context(), accountID, merchantID)
		if err != nil {
			mapError(w, err)
			return core.Principal{}, false
		}
		if !owned {
			writeError(w, http.StatusForbidden, "forbidden", "merchant does not belong to account")
			return core.Principal{}, false
		}
	}
	return core.Principal{AccountID: accountID, MerchantID: merchantID}, true
}

func (s *Server) dashboardListWebhooks(w http.ResponseWriter, r *http.Request) {
	p, ok := s.dashboardWebhookPrincipal(w, r)
	if !ok {
		return
	}
	result, err := s.webhooks.ListWebhooks(r.Context(), p)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *Server) dashboardCreateWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.dashboardWebhookPrincipal(w, r)
	if !ok {
		return
	}
	var in webhookCreateRequest
	if !decodeWebhookRequest(w, r, &in) || !validateWebhookInput(w, in.URL, in.EventTypes) {
		return
	}
	p, ok = s.selectWebhookMerchant(w, r, p, in.MerchantID)
	if !ok {
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		mapError(w, err)
		return
	}
	result, err := s.webhooks.CreateWebhook(r.Context(), p, in.URL, secret, in.EventTypes)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) dashboardUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.dashboardWebhookPrincipal(w, r)
	if !ok {
		return
	}
	var in webhookPatchRequest
	if !decodeWebhookRequest(w, r, &in) {
		return
	}
	if in.URL == nil && in.EventTypes == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "At least one of url or eventTypes is required.")
		return
	}
	if in.URL != nil && !validWebhookURL(*in.URL) {
		writeError(w, http.StatusBadRequest, "invalid_webhook_url", "The webhook URL must be a valid HTTP or HTTPS URL.")
		return
	}
	if in.EventTypes != nil && !validateWebhookEvents(w, *in.EventTypes) {
		return
	}
	result, err := s.webhooks.UpdateWebhook(r.Context(), p, r.PathValue("webhookId"), in.URL, in.EventTypes)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) dashboardDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.dashboardWebhookPrincipal(w, r)
	if !ok {
		return
	}
	if err := s.webhooks.DeleteWebhook(r.Context(), p, r.PathValue("webhookId")); err != nil {
		mapError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) dashboardRotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	p, ok := s.dashboardWebhookPrincipal(w, r)
	if !ok {
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		mapError(w, err)
		return
	}
	result, err := s.webhooks.RotateWebhookSecret(r.Context(), p, r.PathValue("webhookId"), secret)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) webhookPrincipal(w http.ResponseWriter, r *http.Request, write bool) (core.Principal, bool) {
	if s.webhooks == nil {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "webhook subscriptions are unavailable")
		return core.Principal{}, false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	p, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return p, false
	}
	allowed := []string{"webhooks:read", "payments:read", "payouts:read"}
	if write {
		allowed = []string{"webhooks:write", "payments:write", "payouts:write"}
	}
	if len(p.Scopes) > 0 && !hasAnyScope(p.Scopes, allowed) {
		writeError(w, http.StatusForbidden, "insufficient_scope", "API key does not grant webhook access.")
		return p, false
	}
	if merchantID := strings.TrimSpace(r.URL.Query().Get("merchantId")); merchantID != "" {
		return s.selectWebhookMerchant(w, r, p, merchantID)
	}
	return p, true
}

func (s *Server) selectWebhookMerchant(w http.ResponseWriter, r *http.Request, p core.Principal, merchantID string) (core.Principal, bool) {
	merchantID = strings.TrimSpace(merchantID)
	if merchantID == "" || merchantID == p.MerchantID {
		return p, true
	}
	if p.MerchantID != "" {
		writeError(w, http.StatusForbidden, "merchant_access_denied", "The API key cannot access the requested merchant.")
		return p, false
	}
	authorizer, ok := s.webhooks.(core.WebhookScopeStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "Webhook scope validation is unavailable.")
		return p, false
	}
	owned, err := authorizer.OwnsWebhookScope(r.Context(), p.AccountID, merchantID)
	if err != nil {
		mapError(w, err)
		return p, false
	}
	if !owned {
		writeError(w, http.StatusForbidden, "merchant_access_denied", "The API key cannot access the requested merchant.")
		return p, false
	}
	p.MerchantID = merchantID
	return p, true
}

func decodeWebhookRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			writeError(w, http.StatusBadRequest, "unknown_field", err.Error())
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
		}
		return false
	}
	return true
}

func validateWebhookInput(w http.ResponseWriter, rawURL string, events []string) bool {
	if !validWebhookURL(rawURL) {
		writeError(w, http.StatusBadRequest, "invalid_webhook_url", "The webhook URL must be a valid HTTP or HTTPS URL.")
		return false
	}
	return validateWebhookEvents(w, events)
}

func validateWebhookEvents(w http.ResponseWriter, events []string) bool {
	seen := map[string]bool{}
	for _, event := range events {
		if seen[event] {
			writeError(w, http.StatusBadRequest, "duplicate_webhook_event", "eventTypes contains a duplicate event: "+event)
			return false
		}
		if !slices.Contains(supportedWebhookEvents, event) {
			writeError(w, http.StatusBadRequest, "unsupported_webhook_event", "Unsupported webhook event: "+event)
			return false
		}
		seen[event] = true
	}
	return true
}

func hasAnyScope(got, wanted []string) bool {
	for _, scope := range wanted {
		if slices.Contains(got, scope) {
			return true
		}
	}
	return false
}

func validWebhookURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func newWebhookSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("whsec_%x", raw[:]), nil
}
