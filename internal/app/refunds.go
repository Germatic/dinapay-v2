package app

import (
	"context"
	"strings"

	"github.com/Germatic/dinapay-v2/internal/core"
)

var ErrUnsupported = core.ErrUnsupported

type Refunds struct {
	store  core.PaymentStore
	legacy core.LegacyRefunds
}

func NewRefunds(store core.PaymentStore, legacy core.LegacyRefunds) *Refunds {
	return &Refunds{store: store, legacy: legacy}
}

func (s *Refunds) Create(ctx context.Context, principal core.Principal, credential, paymentID, key string, in core.CreateRefund) (core.Refund, bool, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(in.ExternalID) == "" || strings.TrimSpace(in.Amount) == "" {
		return core.Refund{}, false, ErrInvalid
	}
	p, err := s.store.Get(ctx, principal.AccountID, principal.MerchantID, paymentID)
	if err != nil {
		return core.Refund{}, false, err
	}
	if p.Origin != "v1" || s.legacy == nil {
		return core.Refund{}, false, ErrUnsupported
	}
	return s.legacy.Create(ctx, credential, paymentID, key, in)
}
func (s *Refunds) List(ctx context.Context, principal core.Principal, credential, paymentID string) (core.RefundList, error) {
	p, err := s.store.Get(ctx, principal.AccountID, principal.MerchantID, paymentID)
	if err != nil {
		return core.RefundList{}, err
	}
	if p.Origin != "v1" || s.legacy == nil {
		return core.RefundList{}, ErrUnsupported
	}
	return s.legacy.List(ctx, credential, paymentID)
}
func (s *Refunds) Get(ctx context.Context, credential, refundID string) (core.Refund, error) {
	if s.legacy == nil {
		return core.Refund{}, ErrUnsupported
	}
	return s.legacy.Get(ctx, credential, refundID)
}
