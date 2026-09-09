package core

import "time"

type Customer map[string]any

type CreatePayment struct {
	MerchantID     string
	AccountID      string
	ExternalID     string
	Amount         string
	Currency       string
	PaymentMethod  string
	Description    string
	Customer       Customer
	SuccessURL     string
	CancelURL      string
	ExpirationDate time.Time
	Metadata       map[string]any
}

type Payment struct {
	TransactionID  string         `json:"transactionId"`
	MerchantID     string         `json:"-"`
	AccountID      string         `json:"-"`
	ExternalID     string         `json:"externalId"`
	Status         string         `json:"status"`
	Amount         string         `json:"amount"`
	Currency       string         `json:"currency"`
	PaymentMethod  string         `json:"paymentMethod"`
	Description    string         `json:"description,omitempty"`
	CreationDate   time.Time      `json:"creationDate"`
	ExpirationDate time.Time      `json:"expirationDate"`
	ActionURL      string         `json:"actionUrl"`
	Customer       Customer       `json:"customer,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	PaymentData    map[string]any `json:"paymentData"`
	Route          RouteDecision  `json:"-"`
	Version        int64          `json:"-"`
}

type RouteRequest struct {
	RequestID        string   `json:"requestId"`
	TransactionID    string   `json:"transactionId"`
	AccountID        string   `json:"accountId,omitempty"`
	MerchantID       string   `json:"merchantId"`
	Operation        string   `json:"operation"`
	Amount           string   `json:"amount"`
	Currency         string   `json:"currency"`
	MarketCountry    string   `json:"marketCountry"`
	PaymentMethod    string   `json:"paymentMethod"`
	Rail             string   `json:"rail,omitempty"`
	DestinationMode  string   `json:"destinationMode,omitempty"`
	RequiredFeatures []string `json:"requiredFeatures,omitempty"`
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
	Status               string         `json:"status"`
	ExpiresAt            time.Time      `json:"expiresAt"`
	Completion           map[string]any `json:"completion"`
}

type MerchantEvent struct {
	EventID         string
	EventType       string
	MerchantID      string
	ResourceID      string
	ResourceVersion int64
	Payload         []byte
}
