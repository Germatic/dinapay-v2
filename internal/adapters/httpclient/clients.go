package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type Router struct {
	baseURL, token string
	client         *http.Client
}

func NewRouter(baseURL, token string) *Router {
	return &Router{baseURL: strings.TrimRight(baseURL, "/"), token: token, client: &http.Client{Timeout: 3 * time.Second}}
}

func (r *Router) Resolve(ctx context.Context, in core.RouteRequest) (core.RouteDecision, error) {
	var out core.RouteDecision
	if err := postJSON(ctx, r.client, r.baseURL+"/v1/routes/resolve", r.token, "", in, &out); err != nil {
		return out, err
	}
	return out, nil
}

type Connectors struct {
	urls   map[string]string
	token  string
	client *http.Client
}

func NewConnectors(urls map[string]string, token string) *Connectors {
	return &Connectors{urls: urls, token: token, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Connectors) CreatePayment(ctx context.Context, route core.RouteDecision, p core.Payment, successURL, cancelURL string) (core.ProviderPayment, error) {
	var out core.ProviderPayment
	base, ok := c.urls[route.ConnectorID]
	if !ok {
		return out, fmt.Errorf("unknown connector %q", route.ConnectorID)
	}
	command := map[string]any{
		"operationId": "payment:" + p.TransactionID + ":create", "transactionId": p.TransactionID,
		"provider": route.Provider, "providerConnectionId": route.ProviderConnectionID,
		"amount": p.Amount, "currency": p.Currency, "paymentMethod": p.PaymentMethod,
		"rail": route.Rail, "destinationMode": route.DestinationMode,
		"customer": p.Customer,
	}
	if p.Description != "" {
		command["description"] = p.Description
	}
	if len(p.Metadata) > 0 {
		command["metadata"] = p.Metadata
	}
	if route.Binding != nil {
		command["binding"] = route.Binding
	}
	if !p.ExpirationDate.IsZero() {
		command["expiresAt"] = p.ExpirationDate
	}
	if successURL != "" || cancelURL != "" {
		urls := map[string]string{}
		if successURL != "" {
			urls["success"] = successURL
		}
		if cancelURL != "" {
			urls["cancel"] = cancelURL
		}
		command["returnUrls"] = urls
	}
	err := postJSON(ctx, c.client, strings.TrimRight(base, "/")+"/v1/payments", c.token, command["operationId"].(string), command, &out)
	return out, err
}

func (c *Connectors) CreateRefund(ctx context.Context, route core.RouteDecision, p core.Payment, r core.Refund, key string) (core.ProviderRefund, error) {
	var out core.ProviderRefund
	base, ok := c.urls[route.ConnectorID]
	if !ok {
		return out, fmt.Errorf("unknown connector %q", route.ConnectorID)
	}
	command := map[string]any{"operationId": "refund:" + r.RefundID + ":create", "refundId": r.RefundID, "transactionId": p.TransactionID, "providerConnectionId": route.ProviderConnectionID, "amount": r.Amount, "currency": r.Currency}
	if r.Reason != "" {
		command["reason"] = r.Reason
	}
	if len(r.Metadata) > 0 {
		command["metadata"] = r.Metadata
	}
	err := postJSON(ctx, c.client, strings.TrimRight(base, "/")+"/v1/payments/"+p.ProviderPaymentID+"/refunds", c.token, key, command, &out)
	return out, err
}
func (c *Connectors) GetRefund(ctx context.Context, route core.RouteDecision, r core.Refund) (core.ProviderRefund, error) {
	var out core.ProviderRefund
	base, ok := c.urls[route.ConnectorID]
	if !ok {
		return out, fmt.Errorf("unknown connector %q", route.ConnectorID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/refunds/"+r.RefundID, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Provider-Connection-Id", route.ProviderConnectionID)
	resp, err := c.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return out, fmt.Errorf("upstream status %d: %s", resp.StatusCode, b)
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

func postJSON(ctx context.Context, client *http.Client, url, token, idempotencyKey string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if trace := Traceparent(ctx); trace != "" {
		req.Header.Set("traceparent", trace)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upstream status %d: %s", resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type traceKey struct{}

func WithTraceparent(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, traceKey{}, value)
}
func Traceparent(ctx context.Context) string {
	value, _ := ctx.Value(traceKey{}).(string)
	return value
}
