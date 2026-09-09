package app

import (
	"context"
	"errors"
	"github.com/Germatic/dinapay-v2/internal/core"
	"log/slog"
	"strings"
	"time"
)

var ErrUnsupported = core.ErrUnsupported

type Refunds struct {
	store     core.PaymentStore
	native    core.RefundStore
	legacy    core.LegacyRefunds
	connector core.Connector
	ledger    core.Ledger
}

func NewRefunds(store core.PaymentStore, native core.RefundStore, legacy core.LegacyRefunds, connector core.Connector, ledger core.Ledger) *Refunds {
	return &Refunds{store: store, native: native, legacy: legacy, connector: connector, ledger: ledger}
}
func (s *Refunds) Create(ctx context.Context, p core.Principal, credential, paymentID, key string, in core.CreateRefund) (core.Refund, bool, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(in.ExternalID) == "" || strings.TrimSpace(in.Amount) == "" {
		return core.Refund{}, false, ErrInvalid
	}
	payment, err := s.store.Get(ctx, p.AccountID, p.MerchantID, paymentID)
	if err != nil {
		return core.Refund{}, false, err
	}
	if payment.Origin == "v1" {
		if s.legacy == nil {
			return core.Refund{}, false, ErrUnsupported
		}
		return s.legacy.Create(ctx, credential, paymentID, key, in)
	}
	if s.native == nil {
		return core.Refund{}, false, ErrUnsupported
	}
	r, replayed, err := s.native.CreateRefund(ctx, p, paymentID, key, in)
	if err == nil && !replayed {
		s.process(ctx, r)
	}
	return r, replayed, err
}
func (s *Refunds) List(ctx context.Context, p core.Principal, credential, paymentID string) (core.RefundList, error) {
	payment, err := s.store.Get(ctx, p.AccountID, p.MerchantID, paymentID)
	if err != nil {
		return core.RefundList{}, err
	}
	if payment.Origin == "v1" {
		if s.legacy == nil {
			return core.RefundList{}, ErrUnsupported
		}
		return s.legacy.List(ctx, credential, paymentID)
	}
	return s.native.ListRefunds(ctx, p, paymentID)
}
func (s *Refunds) Get(ctx context.Context, p core.Principal, credential, id string) (core.Refund, error) {
	if s.native != nil {
		r, err := s.native.GetRefund(ctx, p, id)
		if err == nil {
			return r, nil
		}
		if !errors.Is(err, core.ErrNotFound) {
			return r, err
		}
	}
	if s.legacy == nil {
		return core.Refund{}, ErrUnsupported
	}
	return s.legacy.Get(ctx, credential, id)
}
func (s *Refunds) Run(ctx context.Context) {
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
func (s *Refunds) runBatch(ctx context.Context) {
	if s.native == nil {
		return
	}
	items, err := s.native.ClaimRefunds(ctx, 50)
	if err != nil {
		slog.Error("refund claim failed", "error", err)
		return
	}
	for _, r := range items {
		s.process(ctx, r)
	}
}
func transition(expected string) map[string]any { return map[string]any{"expectedStatus": expected} }
func (s *Refunds) process(ctx context.Context, r core.Refund) {
	switch r.OperationalStatus {
	case "pending_debit":
		if s.ledger == nil {
			return
		}
		if err := s.ledger.DebitConfirmedRefund(ctx, r.AccountID, r.RefundID, r.Amount, r.Currency); err != nil {
			if errors.Is(err, core.ErrInsufficientBalance) {
				f := transition("pending_debit")
				f["lastError"] = "insufficient balance"
				_, _ = s.native.TransitionRefund(ctx, r.RefundID, "failed", f)
			}
			return
		}
		f := transition("pending_debit")
		f["balanceDebited"] = true
		_, _ = s.native.TransitionRefund(ctx, r.RefundID, "pending_provider", f)
	case "pending_provider":
		p := core.Payment{TransactionID: r.TransactionID, AccountID: r.AccountID, MerchantID: r.MerchantID, ProviderPaymentID: r.ProviderPaymentID, Route: r.Route}
		result, err := s.connector.CreateRefund(ctx, r.Route, p, r, "refund:"+r.RefundID)
		if err != nil {
			result, err = s.connector.GetRefund(ctx, r.Route, r)
			if err != nil {
				return
			}
		}
		s.applyProvider(ctx, r, result)
	case "pending":
		result, err := s.connector.GetRefund(ctx, r.Route, r)
		if err == nil {
			s.applyProvider(ctx, r, result)
		}
	case "pending_compensation":
		if s.ledger == nil || s.ledger.CreditFailedRefund(ctx, r.AccountID, r.RefundID, r.Amount, r.Currency) != nil {
			return
		}
		_, _ = s.native.TransitionRefund(ctx, r.RefundID, "failed", transition("pending_compensation"))
	}
}
func (s *Refunds) applyProvider(ctx context.Context, r core.Refund, p core.ProviderRefund) {
	next := "pending"
	if p.Status == "confirmed" {
		next = "succeeded"
	} else if p.Status == "failed" || p.Status == "rejected" {
		next = "pending_compensation"
	}
	f := transition(r.OperationalStatus)
	f["providerSubmitted"] = true
	f["providerRefundId"] = p.ProviderRefundID
	f["providerStatus"] = p.RawStatus
	_, _ = s.native.TransitionRefund(ctx, r.RefundID, next, f)
}
