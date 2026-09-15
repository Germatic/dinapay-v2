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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || !validWebhookURL(in.URL) || !validWebhookEvents(in.EventTypes) {
		writeError(w, http.StatusBadRequest, "invalid_request", "url or eventTypes is invalid")
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || (in.URL == nil && in.EventTypes == nil) || (in.URL != nil && !validWebhookURL(*in.URL)) || (in.EventTypes != nil && !validWebhookEvents(*in.EventTypes)) {
		writeError(w, http.StatusBadRequest, "invalid_request", "url or eventTypes is invalid")
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
		writeError(w, http.StatusForbidden, "forbidden", "API key does not grant webhook access")
		return p, false
	}
	return p, true
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

func validWebhookEvents(events []string) bool {
	seen := map[string]bool{}
	for _, event := range events {
		if !slices.Contains(supportedWebhookEvents, event) || seen[event] {
			return false
		}
		seen[event] = true
	}
	return true
}

func newWebhookSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("whsec_%x", raw[:]), nil
}
