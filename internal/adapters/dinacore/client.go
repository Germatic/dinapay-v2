package dinacore

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

// Client adapts the current Dinacore balance endpoints to the V2 Ledger port.
// RefID makes both operations idempotent in Dinacore.
type Client struct {
	baseURL, apiKey string
	http            *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) CreditConfirmedPayment(ctx context.Context, p core.Payment, amount string) error {
	return c.CreditBalance(ctx, p.AccountID, p.TransactionID, amount, p.Currency)
}

func (c *Client) CreditBalance(ctx context.Context, accountID, refID, amount, currency string) error {
	return c.post(ctx, "/api/balance/credit", map[string]string{"merchantId": accountID, "currency": currency, "amount": amount, "refType": "cashin", "refId": refID})
}

func (c *Client) DebitConfirmedRefund(ctx context.Context, merchantID, refundID, amount, currency string) error {
	return c.post(ctx, "/api/balance/debit", map[string]string{"merchantId": merchantID, "currency": currency, "amount": amount, "refType": "refund", "refId": refundID})
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-Api-Key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("dinacore status %d: %s", resp.StatusCode, message)
	}
	return nil
}
