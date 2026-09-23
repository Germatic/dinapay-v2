package arsalias

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type CoinagConfig struct {
	BaseURL, TokenURL, ClientID, ClientSecret, Username, Password string
}

type Coinag struct {
	cfg       CoinagConfig
	http      *http.Client
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func NewCoinag(cfg CoinagConfig) (*Coinag, error) {
	if cfg.BaseURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("incomplete Coinag alias resolver configuration")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 16
	return &Coinag{cfg: cfg, http: &http.Client{Timeout: 10 * time.Second, Transport: transport}}, nil
}

func (c *Coinag) ResolveAlias(ctx context.Context, alias string) (core.ResolvedBankAccount, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return core.ResolvedBankAccount{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.BaseURL, "/")+"/coelsapsp/v1/Alias/"+url.PathEscape(alias), nil)
	if err != nil {
		return core.ResolvedBankAccount{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return core.ResolvedBankAccount{}, fmt.Errorf("Coinag alias lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return core.ResolvedBankAccount{}, &core.AliasResolutionError{Message: fmt.Sprintf("alias lookup status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))), Permanent: resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests}
	}
	var out struct {
		Cuenta struct {
			CBU string `json:"cbu"`
		} `json:"cuenta"`
		Titulares []struct {
			CUIT   string `json:"cuit"`
			Nombre string `json:"nombre"`
		} `json:"titulares"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return core.ResolvedBankAccount{}, err
	}
	if len(out.Titulares) == 0 || len(out.Cuenta.CBU) != 22 {
		return core.ResolvedBankAccount{}, &core.AliasResolutionError{Message: "alias lookup returned incomplete account data", Permanent: true}
	}
	return core.ResolvedBankAccount{AccountNumber: out.Cuenta.CBU, TaxID: strings.TrimSpace(out.Titulares[0].CUIT), HolderName: strings.TrimSpace(out.Titulares[0].Nombre)}, nil
}

func (c *Coinag) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Add(30*time.Second).Before(c.expiresAt) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"password"}, "username": {c.cfg.Username}, "password": {c.cfg.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.cfg.ClientID+":"+c.cfg.ClientSecret)))
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("Coinag token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("Coinag token status %d", resp.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.AccessToken == "" {
		return "", errors.New("invalid Coinag token response")
	}
	c.token = result.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return c.token, nil
}
