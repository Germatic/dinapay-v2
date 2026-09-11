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
	ReceivedAmount    string         `json:"receivedAmount,omitempty"`
	Currency          string         `json:"currency"`
	PaymentMethod     string         `json:"paymentMethod"`
	Description       string         `json:"description,omitempty"`
	CreationDate      time.Time      `json:"creationDate"`
	ExpirationDate    time.Time      `json:"expirationDate"`
	ConfirmationDate  *time.Time     `json:"confirmationDate,omitempty"`
	ActionURL         string         `json:"actionUrl"`
	Customer          Customer       `json:"customer,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	PaymentData       map[string]any `json:"paymentData"`
	Pricing           map[string]any `json:"pricing,omitempty"`
	ProviderPaymentID string         `json:"-"`
	ProviderReference string         `json:"-"`
	Route             RouteDecision  `json:"-"`
	Version           int64          `json:"-"`
	Origin            string         `json:"-"`
}

type PaymentListOptions struct {
	Limit                                                        int
	Cursor, Status, Currency                                     string
	ExternalID                                                   string
	CreatedAfter, CreatedBefore, ConfirmedAfter, ConfirmedBefore *time.Time
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

type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type PayoutDestination struct {
	Country     string         `json:"country"`
	Currency    string         `json:"currency"`
	Amount      string         `json:"amount,omitempty"`
	Beneficiary map[string]any `json:"beneficiary"`
	Rail        map[string]any `json:"rail"`
}

type CreatePayout struct {
	MerchantID  string            `json:"merchantId,omitempty"`
	ExternalID  string            `json:"externalId"`
	Source      Money             `json:"source"`
	Destination PayoutDestination `json:"destination"`
	Remitter    map[string]any    `json:"remitter,omitempty"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
}

type Payout struct {
	PayoutID          string            `json:"payoutId"`
	ExternalID        string            `json:"externalId"`
	Source            Money             `json:"source"`
	Destination       PayoutDestination `json:"destination"`
	Pricing           map[string]any    `json:"pricing,omitempty"`
	Remitter          map[string]any    `json:"remitter,omitempty"`
	Description       string            `json:"description,omitempty"`
	Status            string            `json:"status"`
	BankSystemTrxID   string            `json:"bankSystemTrxId,omitempty"`
	CreationDate      time.Time         `json:"creationDate"`
	ConfirmationDate  *time.Time        `json:"confirmationDate,omitempty"`
	FailureDate       *time.Time        `json:"failureDate,omitempty"`
	CancellationDate  *time.Time        `json:"cancellationDate,omitempty"`
	ReversalDate      *time.Time        `json:"reversalDate,omitempty"`
	Failure           map[string]any    `json:"failure,omitempty"`
	Metadata          map[string]any    `json:"metadata,omitempty"`
	AccountID         string            `json:"-"`
	MerchantID        string            `json:"-"`
	ProviderPayoutID  string            `json:"-"`
	Route             RouteDecision     `json:"-"`
	BalanceDebited    bool              `json:"-"`
	ProviderSubmitted bool              `json:"-"`
	ResourceVersion   int64             `json:"-"`
	NextAttemptAt     time.Time         `json:"-"`
	OperationalStatus string            `json:"-"`
}

type PayoutListOptions struct {
	Limit                                                        int
	Cursor, Status, Currency, ExternalID                         string
	CreatedAfter, CreatedBefore, ConfirmedAfter, ConfirmedBefore *time.Time
}
type PayoutPage struct {
	Data       []Payout `json:"data"`
	HasMore    bool     `json:"hasMore"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

type DashboardSummaryOptions struct {
	MerchantID, Direction, Status, Currency, ExternalID          string
	CreatedAfter, CreatedBefore, ConfirmedAfter, ConfirmedBefore *time.Time
}

type DashboardCurrencySummary struct {
	Currency            string  `json:"currency"`
	PayinCount          int64   `json:"payinCount"`
	PayinVolume         string  `json:"payinVolume"`
	PayinSettledCount   int64   `json:"payinSettledCount"`
	PayinSettledVolume  string  `json:"payinSettledVolume"`
	PayoutCount         int64   `json:"payoutCount"`
	PayoutVolume        string  `json:"payoutVolume"`
	PayoutSettledCount  int64   `json:"payoutSettledCount"`
	PayoutSettledVolume string  `json:"payoutSettledVolume"`
	PlatformFeeEarned   *string `json:"platformFeeEarned"`
	DinariaFeeCharged   *string `json:"dinariaFeeCharged"`
	FeesComplete        bool    `json:"feesComplete"`
}

type DashboardSummary struct {
	Period struct {
		From string `json:"from,omitempty"`
		To   string `json:"to,omitempty"`
	} `json:"period"`
	Currencies      []DashboardCurrencySummary `json:"currencies"`
	SubAccountCount int64                      `json:"subAccountCount"`
	AccountCount    int64                      `json:"accountCount,omitempty"`
}

type RouteRequest struct {
	RequestID           string   `json:"requestId"`
	TransactionID       string   `json:"transactionId"`
	AccountID           string   `json:"accountId,omitempty"`
	MerchantID          string   `json:"merchantId"`
	Operation           string   `json:"operation"`
	Amount              string   `json:"amount"`
	Currency            string   `json:"currency"`
	DestinationCurrency string   `json:"destinationCurrency,omitempty"`
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
type ProviderPayout struct {
	PayoutID             string         `json:"payoutId"`
	Provider             string         `json:"provider"`
	ProviderConnectionID string         `json:"providerConnectionId"`
	ProviderPayoutID     string         `json:"providerPayoutId"`
	ProviderReference    string         `json:"providerReference,omitempty"`
	Status               string         `json:"status"`
	RawStatus            string         `json:"rawStatus,omitempty"`
	Source               Money          `json:"source"`
	DestinationAmount    string         `json:"destinationAmount,omitempty"`
	DestinationCurrency  string         `json:"destinationCurrency,omitempty"`
	Pricing              map[string]any `json:"pricing,omitempty"`
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
	TransactionID        string            `json:"transactionId,omitempty"`
	RefundID             string            `json:"refundId,omitempty"`
	PayoutID             string            `json:"payoutId,omitempty"`
	Provider             string            `json:"provider"`
	ProviderConnectionID string            `json:"providerConnectionId"`
	ProviderPaymentID    string            `json:"providerPaymentId,omitempty"`
	ProviderRefundID     string            `json:"providerRefundId,omitempty"`
	ProviderPayoutID     string            `json:"providerPayoutId,omitempty"`
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
