package core

import "time"

type Customer map[string]any

type CreatePayment struct {
	MerchantID      string
	AccountID       string
	ExternalID      string
	Amount          string
	Currency        string
	PaymentMethod   string
	Rail            string
	DestinationMode string
	Description     string
	Customer        Customer
	SuccessURL      string
	CancelURL       string
	ExpirationDate  time.Time
	Metadata        map[string]any
}

type Payment struct {
	TransactionID     string         `json:"transactionId"`
	MerchantID        string         `json:"-"`
	AccountID         string         `json:"-"`
	ExternalID        string         `json:"externalId"`
	Status            string         `json:"status"`
	Amount            string         `json:"amount"`
	Currency          string         `json:"currency"`
	PaymentMethod     string         `json:"paymentMethod"`
	Description       string         `json:"description,omitempty"`
	CreationDate      time.Time      `json:"creationDate"`
	ExpirationDate    time.Time      `json:"expirationDate"`
	ActionURL         string         `json:"actionUrl"`
	Customer          Customer       `json:"customer,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	PaymentData       map[string]any `json:"paymentData"`
	ProviderPaymentID string         `json:"-"`
	ProviderReference string         `json:"-"`
	Route             RouteDecision  `json:"-"`
	Version           int64          `json:"-"`
	Origin            string         `json:"-"`
}

type PaymentListOptions struct {
	Limit  int
	Cursor string
}

type PaymentPage struct {
	Data       []Payment `json:"data"`
	HasMore    bool      `json:"hasMore"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type CreateRefund struct {
	ExternalID string         `json:"externalId"`
	Amount     string         `json:"amount"`
	Reason     string         `json:"reason,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type Refund struct {
	RefundID          string         `json:"refundId"`
	TransactionID     string         `json:"transactionId"`
	ExternalID        string         `json:"externalId"`
	Status            string         `json:"status"`
	Amount            string         `json:"amount"`
	Currency          string         `json:"currency"`
	Reason            string         `json:"reason,omitempty"`
	CreationDate      time.Time      `json:"creationDate"`
	CompletionDate    *time.Time     `json:"completionDate,omitempty"`
	ProviderReference string         `json:"providerReference,omitempty"`
	Failure           map[string]any `json:"failure,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	MerchantID        string         `json:"-"`
	AccountID         string         `json:"-"`
	ProviderPaymentID string         `json:"-"`
	Route             RouteDecision  `json:"-"`
	BalanceDebited    bool           `json:"-"`
	ProviderSubmitted bool           `json:"-"`
	ResourceVersion   int64          `json:"-"`
	NextAttemptAt     time.Time      `json:"-"`
	OperationalStatus string         `json:"-"`
}

type RefundList struct {
	Data []Refund `json:"data"`
}

type RouteRequest struct {
	RequestID           string   `json:"requestId"`
	TransactionID       string   `json:"transactionId"`
	AccountID           string   `json:"accountId,omitempty"`
	MerchantID          string   `json:"merchantId"`
	Operation           string   `json:"operation"`
	Amount              string   `json:"amount"`
	Currency            string   `json:"currency"`
	MarketCountry       string   `json:"marketCountry"`
	PaymentMethod       string   `json:"paymentMethod"`
	CustomerHasDocument bool     `json:"customerHasDocument,omitempty"`
	Rail                string   `json:"rail,omitempty"`
	DestinationMode     string   `json:"destinationMode,omitempty"`
	RequiredFeatures    []string `json:"requiredFeatures,omitempty"`
}

type ProviderBinding struct {
	BindingID          string `json:"bindingId"`
	EntityType         string `json:"entityType"`
	EntityID           string `json:"entityId"`
	ExternalEntityType string `json:"externalEntityType"`
	ExternalEntityID   string `json:"externalEntityId"`
}

type RouteDecision struct {
	RouteDecisionID      string           `json:"routeDecisionId"`
	RequestID            string           `json:"requestId"`
	TransactionID        string           `json:"transactionId"`
	Status               string           `json:"status"`
	ConnectorID          string           `json:"connectorId"`
	Provider             string           `json:"provider"`
	ProviderConnectionID string           `json:"providerConnectionId"`
	Rail                 string           `json:"rail"`
	DestinationMode      string           `json:"destinationMode,omitempty"`
	Binding              *ProviderBinding `json:"binding,omitempty"`
	PolicyVersion        string           `json:"policyVersion"`
	DecidedAt            time.Time        `json:"decidedAt"`
	ReasonCodes          []string         `json:"reasonCodes"`
}

type ProviderPayment struct {
	TransactionID        string         `json:"transactionId"`
	Provider             string         `json:"provider"`
	ProviderConnectionID string         `json:"providerConnectionId"`
	ProviderPaymentID    string         `json:"providerPaymentId"`
	ProviderReference    string         `json:"providerReference,omitempty"`
	Status               string         `json:"status"`
	ExpiresAt            time.Time      `json:"expiresAt"`
	Completion           map[string]any `json:"completion"`
}
type ProviderRefund struct {
	RefundID             string         `json:"refundId"`
	TransactionID        string         `json:"transactionId"`
	Provider             string         `json:"provider"`
	ProviderConnectionID string         `json:"providerConnectionId"`
	ProviderRefundID     string         `json:"providerRefundId"`
	Status               string         `json:"status"`
	RawStatus            string         `json:"rawStatus,omitempty"`
	Amount               string         `json:"amount"`
	Currency             string         `json:"currency"`
	ObservedAt           time.Time      `json:"observedAt"`
	ProviderData         map[string]any `json:"providerData,omitempty"`
}

type MerchantEvent struct {
	EventID         string
	EventType       string
	MerchantID      string
	ResourceID      string
	ResourceVersion int64
	Payload         []byte
}

type ProviderEvent struct {
	EventID              string            `json:"eventId"`
	EventType            string            `json:"eventType"`
	EventVersion         string            `json:"eventVersion"`
	Source               string            `json:"source"`
	OccurredAt           time.Time         `json:"occurredAt"`
	ObservedAt           time.Time         `json:"observedAt"`
	TraceID              string            `json:"traceId,omitempty"`
	TransactionID        string            `json:"transactionId"`
	RefundID             string            `json:"refundId,omitempty"`
	Provider             string            `json:"provider"`
	ProviderConnectionID string            `json:"providerConnectionId"`
	ProviderPaymentID    string            `json:"providerPaymentId"`
	ProviderRefundID     string            `json:"providerRefundId,omitempty"`
	Sequence             *int64            `json:"sequence,omitempty"`
	Data                 ProviderEventData `json:"data"`
}

type ProviderEventData struct {
	Status            string         `json:"status"`
	RawStatus         string         `json:"rawStatus"`
	Amount            string         `json:"amount,omitempty"`
	Currency          string         `json:"currency,omitempty"`
	ProviderReference string         `json:"providerReference,omitempty"`
	ProviderData      map[string]any `json:"providerData,omitempty"`
}

type EventResult struct {
	Duplicate bool   `json:"duplicate"`
	Changed   bool   `json:"changed"`
	Status    string `json:"status,omitempty"`
}
