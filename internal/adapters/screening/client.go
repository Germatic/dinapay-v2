package screening

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type Client struct {
	url, key, environment string
	http                  *http.Client
	failOpen              bool
	observe               func(resource, mode, decision, action, outcome string)
}

func New(baseURL, key, environment string, timeout time.Duration, failOpen bool, observe func(string, string, string, string, string)) *Client {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Client{url: strings.TrimRight(baseURL, "/") + "/internal/v1/evaluations", key: key, environment: environment, http: &http.Client{Timeout: timeout}, failOpen: failOpen, observe: observe}
}

type response struct {
	Mode, Decision, Action string
	ProviderEvidence       struct {
		Provider string `json:"provider"`
	} `json:"providerEvidence"`
}
type errorEnvelope struct {
	Error struct{ Code, Message string } `json:"error"`
}

func (c *Client) Evaluate(ctx context.Context, in core.ScreeningObservation) error {
	subject := normalizeSubject(in.Subject)
	body := map[string]any{"operationId": in.OperationID, "contractVersion": "1", "subject": subject, "context": map[string]any{"accountId": in.AccountID, "merchantId": in.MerchantID, "resourceType": in.ResourceType, "resourceId": in.ResourceID, "country": in.Country, "paymentMethod": in.PaymentMethod, "railType": in.Rail, "environment": c.environment}}
	payload, err := json.Marshal(body)
	if err != nil {
		return c.failure(in, "encode_error", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return c.failure(in, "request_error", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("X-Internal-Key", c.key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return c.failure(in, "unavailable", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e errorEnvelope
		_ = json.Unmarshal(raw, &e)
		code := e.Error.Code
		if code == "" {
			code = "http_error"
		}
		return c.failure(in, code, fmt.Errorf("screening status %d", res.StatusCode))
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return c.failure(in, "invalid_response", err)
	}
	c.record(in.ResourceType, out.Mode, out.Decision, out.Action, "evaluated")
	slog.Info("screening evaluated", "resource", in.ResourceType, "resource_id", in.ResourceID, "merchant_id", in.MerchantID, "mode", out.Mode, "decision", out.Decision, "action", out.Action, "provider", out.ProviderEvidence.Provider)
	switch out.Action {
	case "block":
		return core.ErrScreeningBlocked
	case "manual_review":
		return core.ErrScreeningReview
	default:
		return nil
	}
}

func (c *Client) failure(in core.ScreeningObservation, code string, err error) error {
	c.record(in.ResourceType, "unknown", "error", "continue", code)
	slog.Warn("screening call failed", "resource", in.ResourceType, "resource_id", in.ResourceID, "merchant_id", in.MerchantID, "code", code, "fail_open", c.failOpen, "error", err)
	if c.failOpen {
		return nil
	}
	return err
}
func (c *Client) record(resource, mode, decision, action, outcome string) {
	if c.observe != nil {
		c.observe(resource, mode, decision, action, outcome)
	}
}

func normalizeSubject(value map[string]any) map[string]any {
	result := map[string]any{}
	copyString := func(target string, names ...string) {
		for _, name := range names {
			if v, ok := value[name].(string); ok && strings.TrimSpace(v) != "" {
				result[target] = strings.TrimSpace(v)
				return
			}
		}
	}
	copyString("externalId", "externalId")
	copyString("firstName", "firstName")
	copyString("lastName", "lastName")
	copyString("legalName", "legalName", "businessName")
	copyString("documentType", "documentType")
	copyString("documentNumber", "documentNumber")
	copyString("country", "country")
	copyString("nationality", "nationality")
	copyString("birthDate", "birthDate", "dateOfBirth")
	typeName := "individual"
	if v, ok := value["type"].(string); ok && (strings.EqualFold(v, "company") || strings.EqualFold(v, "business")) {
		typeName = "company"
	}
	result["subjectType"] = typeName
	result["role"] = "customer"
	if typeName == "individual" {
		if _, ok := result["firstName"]; !ok {
			if name, ok := value["name"].(string); ok {
				parts := strings.Fields(name)
				if len(parts) > 0 {
					result["firstName"] = parts[0]
				}
				if len(parts) > 1 {
					result["lastName"] = strings.Join(parts[1:], " ")
				}
			}
		}
	}
	return result
}
