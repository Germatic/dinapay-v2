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
	createdFor    core.Principal
	createErr     error
	owned         bool
}

func (s *webhookStoreStub) CreateWebhook(_ context.Context, p core.Principal, url, secret string, events []string) (core.WebhookSubscription, error) {
	s.createdURL, s.createdEvents, s.createdFor = url, events, p
	if s.createErr != nil {
		return core.WebhookSubscription{}, s.createErr
	}
	return core.WebhookSubscription{WebhookID: "webhook-1", URL: url, APIVersion: "2", Scope: "merchant", EventTypes: events, Secret: secret}, nil
}
func (s *webhookStoreStub) OwnsWebhookScope(context.Context, string, string) (bool, error) {
	return s.owned, nil
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
	if !strings.Contains(w.Body.String(), `"code":"unsupported_webhook_event"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestCreateWebhookAcceptsMerchantForAccountCredential(t *testing.T) {
	store := &webhookStoreStub{owned: true}
	auth := webhookAuthStub{principal: core.Principal{AccountID: "bx-account", Scopes: []string{"webhooks:write"}}}
	h := NewWithDashboardReader(nil, nil, nil, nil, auth, "", nil, "", store)
	req := httptest.NewRequest(http.MethodPost, "/v2/webhooks", strings.NewReader(`{"merchantId":"bx_merchant_1","url":"https://cmpsstchadmpnl.xyz/api/callback/dinaria-ae23bn/payment","eventTypes":["payment.created","payment.status_changed","refund.created","refund.status_changed","payout.created","payout.status_changed"]}`))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if store.createdFor.AccountID != "bx-account" || store.createdFor.MerchantID != "bx_merchant_1" {
		t.Fatalf("principal=%#v", store.createdFor)
	}
}

func TestCreateWebhookRejectsMerchantOutsideAccount(t *testing.T) {
	store := &webhookStoreStub{owned: false}
	auth := webhookAuthStub{principal: core.Principal{AccountID: "account-1"}}
	h := NewWithDashboardReader(nil, nil, nil, nil, auth, "", nil, "", store)
	req := httptest.NewRequest(http.MethodPost, "/v2/webhooks", strings.NewReader(`{"merchantId":"other-merchant","url":"https://merchant.example/events","eventTypes":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"merchant_access_denied"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateWebhookReturnsSpecificValidationErrors(t *testing.T) {
	auth := webhookAuthStub{principal: core.Principal{MerchantID: "merchant-1"}}
	tests := []struct {
		body string
		code string
	}{
		{`{"url":"not-a-url","eventTypes":[]}`, "invalid_webhook_url"},
		{`{"url":"https://merchant.example/events","eventTypes":["payment.created","payment.created"]}`, "duplicate_webhook_event"},
		{`{"url":"https://merchant.example/events","eventTypes":[],"unexpected":true}`, "unknown_field"},
	}
	for _, test := range tests {
		h := NewWithDashboardReader(nil, nil, nil, nil, auth, "", nil, "", &webhookStoreStub{})
		req := httptest.NewRequest(http.MethodPost, "/v2/webhooks", strings.NewReader(test.body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"`+test.code+`"`) {
			t.Fatalf("code=%s status=%d body=%s", test.code, w.Code, w.Body.String())
		}
	}
}

func TestCreateWebhookReportsURLConflict(t *testing.T) {
	store := &webhookStoreStub{createErr: core.ErrWebhookAlreadyExists}
	auth := webhookAuthStub{principal: core.Principal{MerchantID: "merchant-1"}}
	h := NewWithDashboardReader(nil, nil, nil, nil, auth, "", nil, "", store)
	req := httptest.NewRequest(http.MethodPost, "/v2/webhooks", strings.NewReader(`{"url":"https://merchant.example/events","eventTypes":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"webhook_already_exists"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
