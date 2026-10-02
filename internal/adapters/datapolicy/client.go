package datapolicy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type rule struct {
	Path, Presence string
	When           map[string]any
}
type policyContext struct{ ScopeType, ScopeID, Environment, Resource, Country, Currency, PaymentMethod, Rail string }
type policy struct {
	ID, Status, EnforcementMode string
	Context                     policyContext
	Rules                       []rule
}
type connectorRequirements struct {
	Provider, Operation                                                                                     string
	Countries, Currencies, SourceCurrencies, DestinationCurrencies, PaymentMethods, Rails, DestinationModes []string
	Rules                                                                                                   []rule
}
type snapshot struct {
	Version, Environment  string
	Policies              []policy
	ConnectorRequirements []connectorRequirements
}

type Client struct {
	url, token, environment string
	http                    *http.Client
	current                 atomic.Pointer[snapshot]
	observe                 func(resource, provider, path, mode, violation string)
}

func New(baseURL, token, environment string, observe func(string, string, string, string, string)) *Client {
	return &Client{url: strings.TrimRight(baseURL, "/") + "/internal/v1/data-policy-snapshot?environment=" + environment, token: token, environment: environment, http: &http.Client{Timeout: 5 * time.Second}, observe: observe}
}

func (c *Client) Run(ctx context.Context, interval time.Duration) {
	if interval < 5*time.Second {
		interval = 30 * time.Second
	}
	c.refresh(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refresh(ctx)
		}
	}
}

func (c *Client) refresh(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(req)
	if err != nil {
		slog.Warn("data policy refresh failed", "error", err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		slog.Warn("data policy refresh failed", "status", response.StatusCode)
		return
	}
	var next snapshot
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err = decoder.Decode(&next); err != nil {
		slog.Warn("data policy snapshot invalid", "error", err)
		return
	}
	if next.Environment != c.environment || strings.TrimSpace(next.Version) == "" {
		slog.Warn("data policy snapshot identity mismatch")
		return
	}
	c.current.Store(&next)
	slog.Info("data policy snapshot activated", "version", next.Version, "policies", len(next.Policies), "connectorRequirements", len(next.ConnectorRequirements))
}

func (c *Client) Evaluate(_ context.Context, input core.DataPolicyObservation) error {
	current := c.current.Load()
	if current == nil {
		return nil
	}
	rules := resolve(*current, input)
	missing := make([]string, 0)
	forbidden := make([]string, 0)
	for path, item := range rules {
		mode := item.mode
		if mode == "" {
			mode = "observe"
		}
		fieldPresent := present(input.Data, path)
		if required(item, input.Rail) && !fieldPresent {
			if c.observe != nil {
				c.observe(input.Resource, input.Provider, path, mode, "missing")
			}
			slog.Warn("required transaction data missing", "resource", input.Resource, "merchantId", input.MerchantID, "provider", input.Provider, "path", path, "mode", mode, "policyVersion", current.Version)
			if mode == "enforce" {
				missing = append(missing, path)
			}
		}
		if item.Presence == "forbidden" && fieldPresent {
			if c.observe != nil {
				c.observe(input.Resource, input.Provider, path, mode, "forbidden")
			}
			slog.Warn("forbidden transaction data present", "resource", input.Resource, "merchantId", input.MerchantID, "provider", input.Provider, "path", path, "mode", mode, "policyVersion", current.Version)
			if mode == "enforce" {
				forbidden = append(forbidden, path)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &core.MissingRequiredDataError{Fields: missing}
	}
	if len(forbidden) > 0 {
		sort.Strings(forbidden)
		return &core.ForbiddenDataError{Fields: forbidden}
	}
	return nil
}

type effectiveRule struct {
	rule
	mode string
	rank int
}

func resolve(value snapshot, input core.DataPolicyObservation) map[string]effectiveRule {
	result := map[string]effectiveRule{}
	for _, p := range value.Policies {
		if p.Status != "active" || p.Context.Environment != value.Environment || p.Context.Resource != input.Resource || !dimension(p.Context.Country, input.Country) || !dimension(p.Context.Currency, input.Currency) || !dimension(p.Context.PaymentMethod, input.PaymentMethod) || !dimension(p.Context.Rail, input.Rail) {
			continue
		}
		rank := -1
		switch p.Context.ScopeType {
		case "global":
			rank = 0
		case "account":
			if p.Context.ScopeID == input.AccountID {
				rank = 1
			}
		case "merchant":
			if p.Context.ScopeID == input.MerchantID {
				rank = 2
			}
		}
		if rank < 0 {
			continue
		}
		for _, r := range p.Rules {
			existing, ok := result[r.Path]
			if ok && existing.Presence == "required" && r.Presence != "required" {
				continue
			}
			if !ok || rank >= existing.rank {
				result[r.Path] = effectiveRule{rule: r, mode: p.EnforcementMode, rank: rank}
			}
		}
	}
	for _, group := range value.ConnectorRequirements {
		if group.Provider != input.Provider || group.Operation != input.Resource || !member(group.Countries, input.Country) || !connectorCurrency(group, input) || !member(group.PaymentMethods, input.PaymentMethod) || !member(group.Rails, input.Rail) || !member(group.DestinationModes, input.DestinationMode) {
			continue
		}
		for _, r := range group.Rules {
			// Connector requirements are immutable technical preconditions for a
			// selected route. Unlike administrative policies, they are always
			// enforced and cannot be relaxed by an account or merchant policy.
			result[r.Path] = effectiveRule{rule: r, mode: "enforce", rank: 3}
		}
	}
	return result
}

func connectorCurrency(group connectorRequirements, input core.DataPolicyObservation) bool {
	if input.Resource == "payout" {
		return member(group.SourceCurrencies, input.Currency)
	}
	return member(group.Currencies, input.Currency)
}
func dimension(policy, target string) bool {
	return policy == "" || policy == "*" || strings.EqualFold(policy, target)
}
func member(values []string, target string) bool {
	return len(values) == 0 || slices.ContainsFunc(values, func(value string) bool { return strings.EqualFold(value, target) })
}
func required(value effectiveRule, rail string) bool {
	if value.Presence == "required" {
		return true
	}
	if value.Presence != "conditional" {
		return false
	}
	expected, ok := value.When["rail"].(string)
	return ok && strings.EqualFold(expected, rail)
}
func present(data map[string]any, path string) bool {
	var value any = data
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		value, ok = object[part]
		if !ok {
			return false
		}
	}
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	return true
}
