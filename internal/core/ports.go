package core

import "context"

type Router interface {
	Resolve(context.Context, RouteRequest) (RouteDecision, error)
}

type Connector interface {
	CreatePayment(context.Context, RouteDecision, Payment, string, string) (ProviderPayment, error)
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
}

type Principal struct {
	AccountID  string
	MerchantID string
}

type Authenticator interface {
	Authenticate(context.Context, string) (Principal, error)
	ResolveMerchant(context.Context, Principal, string) (string, error)
}
