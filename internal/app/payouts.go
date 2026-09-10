package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type Payouts struct {
	store     core.PayoutStore
	router    core.Router
	connector core.PayoutConnector
	ledger    core.PayoutLedger
	auth      core.Authenticator
}

func NewPayouts(store core.PayoutStore, router core.Router, connector core.PayoutConnector, ledger core.PayoutLedger, auth core.Authenticator) *Payouts {
	return &Payouts{store: store, router: router, connector: connector, ledger: ledger, auth: auth}
}
func (s *Payouts) Create(ctx context.Context, principal core.Principal, key string, in core.CreatePayout) (core.Payout, bool, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(in.ExternalID) == "" || strings.TrimSpace(in.Source.Amount) == "" || strings.TrimSpace(in.Source.Currency) == "" || strings.TrimSpace(in.Destination.Country) == "" || strings.TrimSpace(in.Destination.Currency) == "" || len(in.Destination.Beneficiary) == 0 || strings.TrimSpace(stringValue(in.Destination.Rail, "type")) == "" {
		return core.Payout{}, false, ErrInvalid
	}
	merchantID, err := s.auth.ResolveMerchant(ctx, principal, in.MerchantID)
	if err != nil {
		return core.Payout{}, false, err
	}
	principal.MerchantID = merchantID
	id := deterministicUUID(merchantID + ":payout:" + key)
	existing, replayed, err := s.store.BeginPayout(ctx, principal, key, requestHash(in), id)
	if err != nil || replayed {
		return existing, replayed, err
	}
	completed := false
	defer func() {
		if !completed {
			_ = s.store.ReleasePayout(context.WithoutCancel(ctx), merchantID, key)
		}
	}()
	route, err := s.router.Resolve(ctx, core.RouteRequest{RequestID: deterministicUUID(merchantID + ":payout-route:" + key), TransactionID: id, AccountID: principal.AccountID, MerchantID: merchantID, Operation: "payout", Amount: in.Source.Amount, Currency: in.Source.Currency, DestinationCurrency: in.Destination.Currency, MarketCountry: in.Destination.Country, Rail: stringValue(in.Destination.Rail, "type")})
	if err != nil {
		return core.Payout{}, false, err
	}
	p, err := s.store.CompletePayout(ctx, principal, key, id, in, route)
	if err != nil {
		return core.Payout{}, false, err
	}
	completed = true
	s.process(ctx, p)
	return p, false, nil
}
func stringValue(v map[string]any, key string) string { x, _ := v[key].(string); return x }
func (s *Payouts) Get(ctx context.Context, p core.Principal, id string) (core.Payout, error) {
	return s.store.GetPayout(ctx, p, id)
}
func (s *Payouts) List(ctx context.Context, p core.Principal, o core.PayoutListOptions) (core.PayoutPage, error) {
	return s.store.ListPayouts(ctx, p, o)
}
func (s *Payouts) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		s.runBatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Payouts) runBatch(ctx context.Context) {
	items, err := s.store.ClaimPayouts(ctx, 50)
	if err != nil {
		slog.Error("payout claim failed", "error", err)
		return
	}
	for _, p := range items {
		s.process(ctx, p)
	}
}
func (s *Payouts) process(ctx context.Context, p core.Payout) {
	switch p.OperationalStatus {
	case "pending_debit":
		if s.ledger == nil {
			return
		}
		if err := s.ledger.DebitPayout(ctx, p.AccountID, p.PayoutID, p.Source.Amount, p.Source.Currency); err != nil {
			if errors.Is(err, core.ErrInsufficientBalance) {
				f := transition("pending_debit")
				f["lastError"] = "insufficient balance"
				_, _ = s.store.TransitionPayout(ctx, p.PayoutID, "failed", f)
			}
			return
		}
		f := transition("pending_debit")
		f["balanceDebited"] = true
		_, _ = s.store.TransitionPayout(ctx, p.PayoutID, "pending_provider", f)
	case "pending_provider":
		result, err := s.connector.CreatePayout(ctx, p.Route, p, "payout:"+p.PayoutID)
		if err != nil {
			if errors.Is(err, core.ErrProviderRejected) {
				f := transition("pending_provider")
				f["lastError"] = "provider rejected payout"
				_, _ = s.store.TransitionPayout(ctx, p.PayoutID, "pending_compensation", f)
				return
			}
			result, err = s.connector.GetPayout(ctx, p.Route, p)
			if err != nil {
				f := transition("pending_provider")
				f["lastError"] = "provider create result is ambiguous"
				_, _ = s.store.TransitionPayout(ctx, p.PayoutID, "provider_unknown", f)
				slog.Error("payout provider result ambiguous", "payout_id", p.PayoutID, "provider", p.Route.Provider, "provider_connection_id", p.Route.ProviderConnectionID)
				return
			}
		}
		s.applyProvider(ctx, p, result)
	case "provider_unknown", "processing":
		result, err := s.connector.GetPayout(ctx, p.Route, p)
		if err == nil {
			s.applyProvider(ctx, p, result)
		}
	case "pending_compensation", "pending_compensation_cancelled", "pending_compensation_reversed":
		if s.ledger == nil || s.ledger.CreditFailedPayout(ctx, p.AccountID, p.PayoutID, p.Source.Amount, p.Source.Currency) != nil {
			return
		}
		next := "failed"
		if p.OperationalStatus == "pending_compensation_cancelled" {
			next = "cancelled"
		}
		if p.OperationalStatus == "pending_compensation_reversed" {
			next = "reversed"
		}
		_, _ = s.store.TransitionPayout(ctx, p.PayoutID, next, transition(p.OperationalStatus))
	}
}
func (s *Payouts) applyProvider(ctx context.Context, p core.Payout, result core.ProviderPayout) {
	next := "processing"
	switch result.Status {
	case "confirmed":
		next = "confirmed"
	case "failed", "rejected":
		next = "pending_compensation"
	case "cancelled":
		next = "pending_compensation_cancelled"
	case "reversed":
		next = "pending_compensation_reversed"
	}
	f := transition(p.OperationalStatus)
	f["providerSubmitted"] = true
	f["providerPayoutId"] = result.ProviderPayoutID
	f["providerReference"] = result.ProviderReference
	f["providerStatus"] = result.RawStatus
	f["destinationAmount"] = result.DestinationAmount
	_, _ = s.store.TransitionPayout(ctx, p.PayoutID, next, f)
}
