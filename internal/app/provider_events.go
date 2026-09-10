package app

import (
	"context"
	"strings"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type ProviderEvents struct {
	store   core.PaymentStore
	payouts core.PayoutEventStore
}

func NewProviderEvents(store core.PaymentStore) *ProviderEvents {
	payouts, _ := store.(core.PayoutEventStore)
	return &ProviderEvents{store: store, payouts: payouts}
}

func (s *ProviderEvents) Handle(ctx context.Context, event core.ProviderEvent) (core.EventResult, error) {
	if strings.HasPrefix(event.EventType, "payout.provider_") {
		if s.payouts == nil || event.EventID == "" || event.EventVersion != "1" || event.Provider == "" || event.ProviderConnectionID == "" || event.ProviderPayoutID == "" || event.ObservedAt.IsZero() {
			return core.EventResult{}, ErrInvalid
		}
		if event.PayoutID == "" {
			var err error
			event, err = s.payouts.ResolvePayoutProviderEvent(ctx, event)
			if err != nil {
				return core.EventResult{}, err
			}
		}
		return s.payouts.ApplyPayoutProviderEvent(ctx, event, deterministicUUID("merchant-event:"+event.EventID))
	}
	if event.EventID == "" || event.EventVersion != "1" || event.TransactionID == "" || event.Provider == "" || event.ProviderConnectionID == "" || event.ProviderPaymentID == "" || event.ObservedAt.IsZero() || !strings.HasPrefix(event.EventType, "payment.provider_") {
		return core.EventResult{}, ErrInvalid
	}
	webhookID := deterministicUUID("merchant-event:" + event.EventID)
	return s.store.ApplyProviderEvent(ctx, event, webhookID)
}
