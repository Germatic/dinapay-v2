package dinacore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

type LedgerEntry struct {
	ID         string    `json:"id"`
	MerchantID string    `json:"merchantId"`
	Currency   string    `json:"currency"`
	Amount     string    `json:"amount"`
	RefType    string    `json:"refType"`
	RefID      string    `json:"refId"`
	CreatedAt  time.Time `json:"createdAt"`
}

func New(baseURL, apiKey string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 16
	transport.MaxConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 4 * time.Second
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: &http.Client{Timeout: 5 * time.Second, Transport: transport}}
}

func (c *Client) CreditConfirmedPayment(ctx context.Context, p core.Payment, amount string) error {
	return c.CreditBalance(ctx, p.AccountID, p.TransactionID, amount, p.Currency)
}

func (c *Client) CreditBalance(ctx context.Context, accountID, refID, amount, currency string) error {
	amount, err := normalizeBalanceAmount(amount)
	if err != nil {
		return err
	}
	return c.post(ctx, "/api/balance/credit", map[string]string{"merchantId": accountID, "currency": currency, "amount": amount, "refType": "cashin", "refId": refID})
}

func (c *Client) DebitConfirmedRefund(ctx context.Context, merchantID, refundID, amount, currency string) error {
	amount, err := normalizeBalanceAmount(amount)
	if err != nil {
		return err
	}
	return c.post(ctx, "/api/balance/debit", map[string]string{"merchantId": merchantID, "currency": currency, "amount": amount, "refType": "refund", "refId": refundID})
}
func (c *Client) CreditFailedRefund(ctx context.Context, merchantID, refundID, amount, currency string) error {
	amount, err := normalizeBalanceAmount(amount)
	if err != nil {
		return err
	}
	return c.post(ctx, "/api/balance/refund", map[string]string{"merchantId": merchantID, "currency": currency, "amount": amount, "refType": "refund_reservation_release", "refId": refundID})
}
func (c *Client) DebitPayout(ctx context.Context, accountID, payoutID, amount, currency string) error {
	amount, err := normalizeBalanceAmount(amount)
	if err != nil {
		return err
	}
	return c.post(ctx, "/api/balance/debit", map[string]string{"merchantId": accountID, "currency": currency, "amount": amount, "refType": "payout", "refId": payoutID})
}
func (c *Client) CreditFailedPayout(ctx context.Context, accountID, payoutID, amount, currency string) error {
	amount, err := normalizeBalanceAmount(amount)
	if err != nil {
		return err
	}
	return c.post(ctx, "/api/balance/refund", map[string]string{"merchantId": accountID, "currency": currency, "amount": amount, "refType": "payout_reservation_release", "refId": payoutID})
}

// normalizeBalanceAmount removes PostgreSQL NUMERIC scale padding while
// rejecting values that would require rounding for Dinacore's 8-decimal limit.
func normalizeBalanceAmount(value string) (string, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ".")
	if len(parts) > 2 || value == "" {
		return "", fmt.Errorf("invalid balance amount %q", value)
	}
	integer := parts[0]
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if integer == "" || strings.HasPrefix(integer, "-") || strings.HasPrefix(integer, "+") {
		return "", fmt.Errorf("invalid balance amount %q", value)
	}
	for _, digits := range []string{integer, fraction} {
		for _, digit := range digits {
			if digit < '0' || digit > '9' {
				return "", fmt.Errorf("invalid balance amount %q", value)
			}
		}
	}
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	fraction = strings.TrimRight(fraction, "0")
	if len(fraction) > 8 {
		return "", fmt.Errorf("balance amount %q has more than 8 fractional digits", value)
	}
	if integer == "0" && fraction == "" {
		return "", fmt.Errorf("balance amount must be positive")
	}
	if fraction == "" {
		return integer, nil
	}
	return integer + "." + fraction, nil
}

func (c *Client) LedgerEntries(ctx context.Context, refID string) ([]LedgerEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/balance/ledger/"+url.PathEscape(refID), nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("X-Api-Key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("dinacore ledger status %d: %s", resp.StatusCode, message)
	}
	var payload struct {
		Entries []LedgerEntry `json:"entries"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode dinacore ledger evidence: %w", err)
	}
	return payload.Entries, nil
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
	defer func() { _, _ = io.Copy(io.Discard, resp.Body) }()
	if resp.StatusCode == http.StatusPaymentRequired {
		return core.ErrInsufficientBalance
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("dinacore status %d: %s", resp.StatusCode, message)
	}
	return nil
}
