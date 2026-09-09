package app

import (
	"context"
	"strings"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type ProviderEvents struct{ store core.PaymentStore }

func NewProviderEvents(store core.PaymentStore) *ProviderEvents { return &ProviderEvents{store: store} }

func (s *ProviderEvents) Handle(ctx context.Context, event core.ProviderEvent) (core.EventResult, error) {
	if event.EventID == "" || event.EventVersion != "1" || event.TransactionID == "" || event.Provider == "" || event.ProviderConnectionID == "" || event.ProviderPaymentID == "" || event.ObservedAt.IsZero() || !strings.HasPrefix(event.EventType, "payment.provider_") {
		return core.EventResult{}, ErrInvalid
	}
	webhookID := deterministicUUID("merchant-event:" + event.EventID)
	return s.store.ApplyProviderEvent(ctx, event, webhookID)
}
