package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type webhookAuthStub struct{ principal core.Principal }

func (a webhookAuthStub) Authenticate(context.Context, string) (core.Principal, error) {
	return a.principal, nil
}
func (a webhookAuthStub) ResolveMerchant(context.Context, core.Principal, string) (string, error) {
	return "", nil
}

type webhookStoreStub struct {
	createdURL    string
	createdEvents []string
}

func (s *webhookStoreStub) CreateWebhook(_ context.Context, p core.Principal, url, secret string, events []string) (core.WebhookSubscription, error) {
	s.createdURL, s.createdEvents = url, events
	return core.WebhookSubscription{WebhookID: "webhook-1", URL: url, APIVersion: "2", Scope: "merchant", EventTypes: events, Secret: secret}, nil
}
func (*webhookStoreStub) ListWebhooks(context.Context, core.Principal) ([]core.WebhookSubscription, error) {
	return []core.WebhookSubscription{}, nil
}
func (*webhookStoreStub) UpdateWebhook(context.Context, core.Principal, string, *string, *[]string) (core.WebhookSubscription, error) {
	return core.WebhookSubscription{}, nil
}
func (*webhookStoreStub) DeleteWebhook(context.Context, core.Principal, string) error { return nil }
func (*webhookStoreStub) RotateWebhookSecret(context.Context, core.Principal, string, string) (core.WebhookSubscription, error) {
	return core.WebhookSubscription{}, nil
}

func TestCreateFilteredWebhook(t *testing.T) {
	store := &webhookStoreStub{}
	auth := webhookAuthStub{principal: core.Principal{AccountID: "account-1", MerchantID: "merchant-1", Scopes: []string{"payouts:write"}}}
	h := NewWithDashboardReader(nil, nil, nil, nil, auth, "", nil, "", store)
	req := httptest.NewRequest(http.MethodPost, "/v2/webhooks", strings.NewReader(`{"url":"https://merchant.example/payouts","eventTypes":["payout.created","payout.status_changed"]}`))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if store.createdURL != "https://merchant.example/payouts" || len(store.createdEvents) != 2 {
		t.Fatalf("url=%q events=%v", store.createdURL, store.createdEvents)
	}
	var result core.WebhookSubscription
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil || !strings.HasPrefix(result.Secret, "whsec_") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCreateWebhookRejectsUnknownEvent(t *testing.T) {
	store := &webhookStoreStub{}
	auth := webhookAuthStub{principal: core.Principal{MerchantID: "merchant-1"}}
	h := NewWithDashboardReader(nil, nil, nil, nil, auth, "", nil, "", store)
	req := httptest.NewRequest(http.MethodPost, "/v2/webhooks", strings.NewReader(`{"url":"https://merchant.example/events","eventTypes":["payout.confirmed"]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
