package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type LegacyRefundClient struct {
	base   string
	client *http.Client
}

func NewLegacyRefundClient(base string) *LegacyRefundClient {
	return &LegacyRefundClient{base: strings.TrimRight(base, "/"), client: &http.Client{Timeout: 20 * time.Second}}
}

type legacyRefund struct {
	RefundID         string     `json:"refundId"`
	PaymentID        string     `json:"paymentId"`
	ExternalID       string     `json:"externalId"`
	Amount           string     `json:"amount"`
	Currency         string     `json:"currency"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	ProviderRefundID string     `json:"providerRefundId"`
	ErrorCode        string     `json:"errorCode"`
	ErrorMessage     string     `json:"errorMessage"`
	CreatedAt        time.Time  `json:"createdAt"`
	CompletedAt      *time.Time `json:"completedAt"`
}

func mapLegacyRefund(v legacyRefund) core.Refund {
	r := core.Refund{RefundID: v.RefundID, TransactionID: v.PaymentID, ExternalID: v.ExternalID, Status: mapRefundStatus(v.Status), Amount: v.Amount, Currency: v.Currency, Reason: v.Reason, CreationDate: v.CreatedAt, CompletionDate: v.CompletedAt, ProviderReference: v.ProviderRefundID}
	if v.ErrorCode != "" || v.ErrorMessage != "" {
		r.Failure = map[string]any{"code": v.ErrorCode, "message": v.ErrorMessage}
	}
	return r
}
func mapRefundStatus(v string) string {
	switch v {
	case "succeeded":
		return "succeeded"
	case "failed":
		return "failed"
	default:
		return "pending"
	}
}
func (c *LegacyRefundClient) Create(ctx context.Context, credential, paymentID, key string, in core.CreateRefund) (core.Refund, bool, error) {
	var out legacyRefund
	replay, err := c.do(ctx, http.MethodPost, "/payments/"+paymentID+"/refunds", credential, key, in, &out)
	return mapLegacyRefund(out), replay, err
}
func (c *LegacyRefundClient) List(ctx context.Context, credential, paymentID string) (core.RefundList, error) {
	var out struct {
		Data []legacyRefund `json:"data"`
	}
	_, err := c.do(ctx, http.MethodGet, "/payments/"+paymentID+"/refunds", credential, "", nil, &out)
	result := core.RefundList{Data: make([]core.Refund, 0, len(out.Data))}
	for _, v := range out.Data {
		result.Data = append(result.Data, mapLegacyRefund(v))
	}
	return result, err
}
func (c *LegacyRefundClient) Get(ctx context.Context, credential, refundID string) (core.Refund, error) {
	var out legacyRefund
	_, err := c.do(ctx, http.MethodGet, "/refunds/"+refundID, credential, "", nil, &out)
	return mapLegacyRefund(out), err
}
func (c *LegacyRefundClient) do(ctx context.Context, method, path, credential, key string, body, out any) (bool, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case http.StatusBadRequest:
			return false, fmt.Errorf("%w: legacy refund rejected", core.ErrInvalid)
		case http.StatusNotFound:
			return false, core.ErrNotFound
		case http.StatusConflict:
			return false, core.ErrConflict
		case http.StatusUnprocessableEntity:
			return false, core.ErrUnsupported
		default:
			return false, fmt.Errorf("legacy refund http %d", resp.StatusCode)
		}
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return false, err
	}
	return resp.Header.Get("Idempotent-Replayed") == "true", nil
}
