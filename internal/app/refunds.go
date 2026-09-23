package app

import (
	"context"
	"errors"
	"fmt"
	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
	"github.com/Germatic/dinapay-v2/internal/core"
	"log/slog"
	"strings"
	"time"
)

var ErrUnsupported = core.ErrUnsupported

type Refunds struct {
	store          core.PaymentStore
	native         core.RefundStore
	legacy         core.LegacyRefunds
	connector      core.Connector
	ledger         core.Ledger
	observeFailure func(operation, provider, code string)
}

func (s *Refunds) WithFailureObserver(observer func(operation, provider, code string)) *Refunds {
	s.observeFailure = observer
	return s
}

func (s *Refunds) observe(r core.Refund) {
	if s.observeFailure == nil || r.Failure == nil {
		return
	}
	code, _ := r.Failure["code"].(string)
	s.observeFailure("refund", r.Route.Provider, code)
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
	if err := core.ValidateMoney(in.Amount, payment.Currency, "amount", "currency"); err != nil {
		return core.Refund{}, false, err
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
				f["failure"] = contract.NewRefund(contract.RefundInsufficientFunds)
				updated, transitionErr := s.native.TransitionRefund(ctx, r.RefundID, "failed", f)
				if transitionErr == nil {
					s.observe(updated)
				}
			}
			return
		}
		f := transition("pending_debit")
		f["balanceDebited"] = true
		_, _ = s.native.TransitionRefund(ctx, r.RefundID, "pending_provider", f)
	case "pending_provider":
		p := core.Payment{TransactionID: r.TransactionID, AccountID: r.AccountID, MerchantID: r.MerchantID, ProviderPaymentID: r.ProviderPaymentID, Route: r.Route}
		result, createErr := s.connector.CreateRefund(ctx, r.Route, p, r, "refund:"+r.RefundID)
		if createErr != nil {
			if errors.Is(createErr, core.ErrProviderRejected) {
				failure := contract.NewRefund(contract.RefundRejected)
				providerFailure := contract.ProviderFailure{Code: "provider_rejected", Message: createErr.Error()}
				var rejected *core.ProviderRejectedError
				if errors.As(createErr, &rejected) {
					if rejected.Failure != nil && contract.ValidRefund(*rejected.Failure) {
						failure = *rejected.Failure
					}
					if rejected.ProviderFailure != nil {
						providerFailure = *rejected.ProviderFailure
					}
				}
				f := transition("pending_provider")
				f["lastError"] = createErr.Error()
				f["failure"] = failure
				f["providerFailure"] = providerFailure
				updated, transitionErr := s.native.TransitionRefund(ctx, r.RefundID, "pending_compensation", f)
				if transitionErr == nil {
					s.observe(updated)
				}
				slog.Warn("refund rejected by provider", "refund_id", r.RefundID, "provider", r.Route.Provider, "provider_connection_id", r.Route.ProviderConnectionID, "error", createErr)
				return
			}
			var recoveryErr error
			result, recoveryErr = s.connector.GetRefund(ctx, r.Route, r)
			if recoveryErr != nil {
				// The create call may have reached the provider. Stop automatic
				// submission retries until reconciliation establishes the outcome;
				// retrying a refund blindly can pay the customer twice.
				f := transition("pending_provider")
				f["lastError"] = fmt.Sprintf("create refund: %v; recover refund: %v", createErr, recoveryErr)
				_, transitionErr := s.native.TransitionRefund(ctx, r.RefundID, "provider_unknown", f)
				if transitionErr != nil {
					slog.Error("refund ambiguous transition failed", "refund_id", r.RefundID, "provider", r.Route.Provider, "provider_connection_id", r.Route.ProviderConnectionID, "error", transitionErr)
					return
				}
				slog.Error("refund requires reconciliation", "refund_id", r.RefundID, "transaction_id", r.TransactionID, "provider", r.Route.Provider, "provider_connection_id", r.Route.ProviderConnectionID, "create_error", createErr, "recovery_error", recoveryErr)
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
		updated, err := s.native.TransitionRefund(ctx, r.RefundID, "failed", transition("pending_compensation"))
		if err == nil {
			s.observe(updated)
		}
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
	if next == "pending_compensation" {
		failure, providerFailure := normalizeRefundFailure(p)
		f["failure"] = failure
		f["providerFailure"] = providerFailure
	}
	_, _ = s.native.TransitionRefund(ctx, r.RefundID, next, f)
}

func normalizeRefundFailure(result core.ProviderRefund) (contract.Failure, contract.ProviderFailure) {
	public := contract.NewRefund(contract.RefundUnknownError)
	if result.Failure != nil && contract.ValidRefund(*result.Failure) {
		public = *result.Failure
	}
	provider := contract.ProviderFailure{}
	if result.ProviderFailure != nil {
		provider = *result.ProviderFailure
	} else if result.RawStatus != "" {
		provider.Code = result.RawStatus
	}
	if result.Failure != nil && !contract.ValidRefund(*result.Failure) {
		if provider.Details == nil {
			provider.Details = map[string]any{}
		}
		provider.Details["invalidNormalizedFailure"] = result.Failure
	}
	return public, provider
}
