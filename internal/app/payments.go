package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

var argentinaDocumentPatterns = map[string]*regexp.Regexp{
	"DNI":  regexp.MustCompile(`^[0-9]{7,8}$`),
	"CUIT": regexp.MustCompile(`^[0-9]{11}$`),
	"CUIL": regexp.MustCompile(`^[0-9]{11}$`),
}

func validatePaymentFormats(in core.CreatePayment) error {
	if err := core.ValidateMoney(in.Amount, in.Currency, "amount", "currency"); err != nil {
		return err
	}
	if !strings.EqualFold(in.PaymentMethod, "qr") || !strings.EqualFold(in.Currency, "ARS") {
		return nil
	}
	documentNumber, numberPresent := in.Customer["documentNumber"]
	if !numberPresent || documentNumber == nil || strings.TrimSpace(fmt.Sprint(documentNumber)) == "" {
		return nil
	}
	number, ok := documentNumber.(string)
	if !ok {
		return &core.ValidationError{Field: "customer.documentNumber", Rule: "format", Message: "customer.documentNumber must be a string"}
	}
	documentType, _ := in.Customer["documentType"].(string)
	if pattern, known := argentinaDocumentPatterns[strings.ToUpper(strings.TrimSpace(documentType))]; known && !pattern.MatchString(number) {
		return &core.ValidationError{Field: "customer.documentNumber", Rule: "format", Message: "customer.documentNumber has an invalid format for customer.documentType"}
	}
	return nil
}

func validCollectionKey(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != ':' && c != '-' {
			return false
		}
	}
	return true
}

type Payments struct {
	router       core.Router
	connector    core.Connector
	store        core.PaymentStore
	merchants    core.Authenticator
	checkoutBase string
	now          func() time.Time
}

func NewPayments(router core.Router, connector core.Connector, store core.PaymentStore, merchants core.Authenticator, checkoutBase string) *Payments {
	return &Payments{router: router, connector: connector, store: store, merchants: merchants, checkoutBase: strings.TrimRight(checkoutBase, "/"), now: time.Now}
}

func (s *Payments) Create(ctx context.Context, principal core.Principal, in core.CreatePayment, idempotencyKey string) (core.Payment, bool, error) {
	if strings.TrimSpace(idempotencyKey) == "" || in.ExternalID == "" || in.Amount == "" || in.Currency == "" || in.PaymentMethod == "" || len(in.Customer) == 0 {
		return core.Payment{}, false, ErrInvalid
	}
	if err := validatePaymentFormats(in); err != nil {
		return core.Payment{}, false, err
	}
	if (in.DestinationMode == "reusable" && !validCollectionKey(in.CollectionKey)) || (in.DestinationMode != "reusable" && in.CollectionKey != "") {
		return core.Payment{}, false, ErrInvalid
	}
	if !validReturnURL(in.SuccessURL) || !validReturnURL(in.CancelURL) {
		return core.Payment{}, false, ErrInvalid
	}
	merchantID, err := s.merchants.ResolveMerchant(ctx, principal, in.MerchantID)
	if err != nil {
		return core.Payment{}, false, err
	}
	hash := requestHash(in)
	now := s.now().UTC()
	if in.ExpirationDate.IsZero() {
		in.ExpirationDate = now.Add(15 * time.Minute)
	}
	txID := deterministicUUID(merchantID + ":payment:" + idempotencyKey)
	if existing, replayed, err := s.store.BeginCreate(ctx, merchantID, idempotencyKey, hash, txID, in.ExternalID); err != nil || replayed {
		return existing, replayed, err
	}
	completed := false
	defer func() {
		if !completed {
			_ = s.store.ReleaseCreate(context.WithoutCancel(ctx), merchantID, idempotencyKey)
		}
	}()
	country, _ := in.Customer["country"].(string)
	_, hasDocument := in.Customer["documentNumber"]
	route, err := s.router.Resolve(ctx, core.RouteRequest{
		RequestID: deterministicUUID(merchantID + ":route:" + idempotencyKey), TransactionID: txID, AccountID: principal.AccountID,
		MerchantID: merchantID, Operation: "payment", Amount: in.Amount,
		Currency: in.Currency, MarketCountry: country, PaymentMethod: in.PaymentMethod,
		Rail: in.Rail, DestinationMode: in.DestinationMode,
		CustomerHasDocument: hasDocument,
	})
	if err != nil {
		return core.Payment{}, false, fmt.Errorf("resolve route: %w", err)
	}
	payment := core.Payment{
		TransactionID: txID, MerchantID: merchantID, AccountID: principal.AccountID,
		ExternalID: in.ExternalID, Status: "started", Amount: in.Amount,
		Currency: in.Currency, PaymentMethod: in.PaymentMethod, Description: in.Description,
		CollectionKey: in.CollectionKey,
		CreationDate:  now, ExpirationDate: in.ExpirationDate, SuccessURL: in.SuccessURL, CancelURL: in.CancelURL, Customer: in.Customer,
		Metadata: in.Metadata, Route: route, Version: 1,
	}
	provider, err := s.connector.CreatePayment(ctx, route, payment, in.SuccessURL, in.CancelURL)
	if err != nil {
		return core.Payment{}, false, fmt.Errorf("create provider payment: %w", err)
	}
	payment.PaymentData, err = publicPaymentData(provider.Completion)
	if err != nil {
		return core.Payment{}, false, fmt.Errorf("map provider completion: %w", err)
	}
	payment.ProviderPaymentID = provider.ProviderPaymentID
	payment.ProviderReference = provider.ProviderReference
	if !provider.ExpiresAt.IsZero() {
		payment.ExpirationDate = provider.ExpiresAt
	}
	payment.ActionURL = s.actionURL(payment.PaymentData, txID)
	eventID := deterministicUUID("payment.created:" + txID)
	body, err := json.Marshal(map[string]any{
		"eventId": eventID, "eventType": "payment.created", "apiVersion": "2",
		"merchantId": merchantID, "creationDate": now, "resourceVersion": 1,
		"data": map[string]any{"object": payment},
	})
	if err != nil {
		return core.Payment{}, false, err
	}
	if err := s.store.CompleteCreate(ctx, payment, core.MerchantEvent{
		EventID: eventID, EventType: "payment.created", MerchantID: merchantID,
		ResourceID: txID, ResourceVersion: 1, Payload: body,
	}, idempotencyKey); err != nil {
		return core.Payment{}, false, err
	}
	completed = true
	return payment, false, nil
}

func validReturnURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.User == nil
}

func (s *Payments) Get(ctx context.Context, principal core.Principal, transactionID string) (core.Payment, error) {
	p, err := s.store.Get(ctx, principal.AccountID, principal.MerchantID, transactionID)
	if err == nil {
		s.normalizeRead(&p)
	}
	return p, err
}

func (s *Payments) Checkout(ctx context.Context, transactionID string) (core.CheckoutPayment, error) {
	reader, ok := s.store.(core.CheckoutPaymentReader)
	if !ok {
		return core.CheckoutPayment{}, ErrNotFound
	}
	return reader.GetCheckoutPayment(ctx, transactionID)
}

func (s *Payments) List(ctx context.Context, principal core.Principal, options core.PaymentListOptions) (core.PaymentPage, error) {
	page, err := s.store.List(ctx, principal.AccountID, principal.MerchantID, options)
	if err == nil {
		for i := range page.Data {
			s.normalizeRead(&page.Data[i])
		}
	}
	return page, err
}

func (s *Payments) normalizeRead(p *core.Payment) {
	if p != nil && p.Origin == "v1" && s.checkoutBase != "" {
		p.ActionURL = s.checkoutBase + "/pay/" + p.TransactionID
	}
}

func (s *Payments) actionURL(paymentData map[string]any, transactionID string) string {
	if s.checkoutBase != "" {
		return s.checkoutBase + "/pay/" + transactionID
	}
	redirect, _ := paymentData["redirect"].(map[string]any)
	links, _ := redirect["links"].(map[string]any)
	recommended, _ := redirect["recommendedAlternative"].(string)
	if value, ok := links[recommended].(string); ok && value != "" {
		return value
	}
	for _, name := range []string{"universal", "web", "app"} {
		if value, ok := links[name].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func publicPaymentData(completion map[string]any) (map[string]any, error) {
	typeName, _ := completion["type"].(string)
	switch typeName {
	case "redirect":
		links, _ := completion["links"].(map[string]any)
		if len(links) == 0 {
			return nil, ErrInvalid
		}
		recommended := "web"
		if _, ok := links["universal"]; ok {
			recommended = "universal"
		} else if _, ok := links["app"]; ok {
			recommended = "app"
		}
		redirect := map[string]any{"recommendedAlternative": recommended, "links": links}
		qr := map[string]any{}
		if value, ok := completion["qrContent"]; ok {
			qr["content"] = value
		}
		if value, ok := completion["qrImageUrl"]; ok {
			qr["imageUrl"] = value
		}
		if len(qr) > 0 {
			redirect["qr"] = qr
		}
		return map[string]any{"type": "redirect", "redirect": redirect}, nil
	case "bank_transfer":
		bank := map[string]any{}
		for _, key := range []string{"rail", "destinationMode", "accountIdentifier", "transferReference"} {
			if value, ok := completion[key]; ok {
				bank[key] = value
			}
		}
		return map[string]any{"type": "bank_transfer", "bankTransfer": bank}, nil
	case "qr":
		qr := map[string]any{}
		if value, ok := completion["format"]; ok {
			qr["format"] = value
		}
		if value, ok := completion["content"]; ok {
			qr["content"] = value
		}
		if value, ok := completion["imageBase64"]; ok {
			qr["imageBase64"] = value
		}
		return map[string]any{"type": "qr", "qr": qr}, nil
	default:
		return nil, fmt.Errorf("unsupported completion type %q", typeName)
	}
}

func requestHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func deterministicUUID(value string) string {
	h := sha256.Sum256([]byte(value))
	b := append([]byte(nil), h[:16]...)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
