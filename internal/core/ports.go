package core

import "context"

type Router interface {
	Resolve(context.Context, RouteRequest) (RouteDecision, error)
}

type Connector interface {
	CreatePayment(context.Context, RouteDecision, Payment, string, string) (ProviderPayment, error)
	CreateRefund(context.Context, RouteDecision, Payment, Refund, string) (ProviderRefund, error)
	GetRefund(context.Context, RouteDecision, Refund) (ProviderRefund, error)
}

// LegacyRefunds is a temporary anti-corruption port used while V1 payments
// remain operational. The raw credential is forwarded only to the loopback V1
// API after V2 authentication and is never persisted.
type LegacyRefunds interface {
	Create(context.Context, string, string, string, CreateRefund) (Refund, bool, error)
	List(context.Context, string, string) (RefundList, error)
	Get(context.Context, string, string) (Refund, error)
}

// PaymentStore reserves idempotency before external calls, then commits the
// payment and initial merchant event atomically.
type PaymentStore interface {
	BeginCreate(context.Context, string, string, string, string) (Payment, bool, error)
	CompleteCreate(context.Context, Payment, MerchantEvent, string) error
	ReleaseCreate(context.Context, string, string) error
	Get(context.Context, string, string, string) (Payment, error)
	List(context.Context, string, string, PaymentListOptions) (PaymentPage, error)
	ApplyProviderEvent(context.Context, ProviderEvent, string) (EventResult, error)
}

// Ledger isolates the current Dinacore API from orchestration. It will be used
// by normalized provider-event processing, not during payment creation.
type Ledger interface {
	CreditConfirmedPayment(context.Context, Payment, string) error
	DebitConfirmedRefund(context.Context, string, string, string, string) error
	CreditFailedRefund(context.Context, string, string, string, string) error
}

type RefundStore interface {
	CreateRefund(context.Context, Principal, string, string, CreateRefund) (Refund, bool, error)
	GetRefund(context.Context, Principal, string) (Refund, error)
	ListRefunds(context.Context, Principal, string) (RefundList, error)
	ClaimRefunds(context.Context, int) ([]Refund, error)
	TransitionRefund(context.Context, string, string, map[string]any) (Refund, error)
}

type Principal struct {
	AccountID  string
	MerchantID string
	Scopes     []string
}

type Authenticator interface {
	Authenticate(context.Context, string) (Principal, error)
	ResolveMerchant(context.Context, Principal, string) (string, error)
}
