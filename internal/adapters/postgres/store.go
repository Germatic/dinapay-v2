package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_initial.sql
var initialMigration string

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.Exec(ctx, initialMigration)
	return err
}

func (s *Store) BeginCreate(ctx context.Context, merchantID, key, hash, transactionID string) (core.Payment, bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO dinapay_v2_idempotency
      (merchant_id,idempotency_key,request_hash,transaction_id,status)
      VALUES ($1,$2,$3,$4,'pending') ON CONFLICT DO NOTHING`, merchantID, key, hash, transactionID)
	if err != nil {
		return core.Payment{}, false, err
	}
	if tag.RowsAffected() == 1 {
		return core.Payment{}, false, nil
	}
	var storedHash, storedID, status string
	if err := s.db.QueryRow(ctx, `SELECT request_hash,transaction_id::text,status FROM dinapay_v2_idempotency WHERE merchant_id=$1 AND idempotency_key=$2`, merchantID, key).Scan(&storedHash, &storedID, &status); err != nil {
		return core.Payment{}, false, err
	}
	if storedHash != hash {
		return core.Payment{}, false, core.ErrConflict
	}
	if status != "complete" {
		return core.Payment{}, false, core.ErrInProgress
	}
	p, err := s.Get(ctx, "", merchantID, storedID)
	return p, err == nil, err
}

func (s *Store) CompleteCreate(ctx context.Context, p core.Payment, event core.MerchantEvent, key string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	customer, _ := json.Marshal(p.Customer)
	metadata, _ := json.Marshal(p.Metadata)
	paymentData, _ := json.Marshal(p.PaymentData)
	route, _ := json.Marshal(p.Route)
	_, err = tx.Exec(ctx, `INSERT INTO dinapay_v2_payments
      (transaction_id,account_id,merchant_id,external_id,status,amount,currency,payment_method,description,creation_date,expiration_date,action_url,customer,metadata,payment_data,provider_payment_id,provider_reference,route_decision,resource_version)
      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,$15,$16,NULLIF($17,''),$18,$19)`,
		p.TransactionID, p.AccountID, p.MerchantID, p.ExternalID, p.Status, p.Amount, p.Currency, p.PaymentMethod, p.Description, p.CreationDate, p.ExpirationDate, p.ActionURL, customer, metadata, paymentData, p.ProviderPaymentID, p.ProviderReference, route, p.Version)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries (webhook_id,event_id,event_type,payload)
      SELECT id,$1,$2,$3 FROM webhooks
      WHERE api_version='2' AND webhook_secret IS NOT NULL AND webhook_secret<>''
        AND (merchant_id=$4 OR (account_id=$5 AND merchant_id IS NULL))
      ON CONFLICT (webhook_id,event_id) DO NOTHING`, event.EventID, event.EventType, event.Payload, p.MerchantID, p.AccountID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE dinapay_v2_idempotency SET status='complete',updated_at=now()
      WHERE merchant_id=$1 AND idempotency_key=$2 AND transaction_id=$3 AND status='pending'`, p.MerchantID, key, p.TransactionID)
	if err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return core.ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *Store) ReleaseCreate(ctx context.Context, merchantID, key string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM dinapay_v2_idempotency WHERE merchant_id=$1 AND idempotency_key=$2 AND status='pending'`, merchantID, key)
	return err
}

func (s *Store) Get(ctx context.Context, accountID, merchantID, transactionID string) (core.Payment, error) {
	var p core.Payment
	var customer, metadata, paymentData, route []byte
	err := s.db.QueryRow(ctx, `SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,currency,payment_method,COALESCE(description,''),creation_date,expiration_date,action_url,customer,metadata,payment_data,provider_payment_id,COALESCE(provider_reference,''),route_decision,resource_version
      FROM dinapay_v2_payments WHERE transaction_id=$1 AND (($2<>'' AND merchant_id=$2) OR ($2='' AND $3<>'' AND account_id=$3))`, transactionID, merchantID, accountID).Scan(
		&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.Currency, &p.PaymentMethod, &p.Description, &p.CreationDate, &p.ExpirationDate, &p.ActionURL, &customer, &metadata, &paymentData, &p.ProviderPaymentID, &p.ProviderReference, &route, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, core.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(customer, &p.Customer)
	_ = json.Unmarshal(metadata, &p.Metadata)
	_ = json.Unmarshal(paymentData, &p.PaymentData)
	_ = json.Unmarshal(route, &p.Route)
	return p, nil
}

func (s *Store) ApplyProviderEvent(ctx context.Context, event core.ProviderEvent, merchantEventID string) (core.EventResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return core.EventResult{}, err
	}
	defer tx.Rollback(ctx)
	payload, _ := json.Marshal(event)
	tag, err := tx.Exec(ctx, `INSERT INTO dinapay_v2_provider_events(event_id,event_type,transaction_id,provider,source,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, event.EventID, event.EventType, event.TransactionID, event.Provider, event.Source, payload)
	if err != nil {
		return core.EventResult{}, err
	}
	if tag.RowsAffected() == 0 {
		return core.EventResult{Duplicate: true}, tx.Commit(ctx)
	}
	var p core.Payment
	var customer, metadata, paymentData, route []byte
	err = tx.QueryRow(ctx, `SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,currency,payment_method,COALESCE(description,''),creation_date,expiration_date,action_url,customer,metadata,payment_data,provider_payment_id,COALESCE(provider_reference,''),route_decision,resource_version FROM dinapay_v2_payments WHERE transaction_id=$1 FOR UPDATE`, event.TransactionID).Scan(&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.Currency, &p.PaymentMethod, &p.Description, &p.CreationDate, &p.ExpirationDate, &p.ActionURL, &customer, &metadata, &paymentData, &p.ProviderPaymentID, &p.ProviderReference, &route, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.EventResult{}, core.ErrNotFound
	}
	if err != nil {
		return core.EventResult{}, err
	}
	_ = json.Unmarshal(customer, &p.Customer)
	_ = json.Unmarshal(metadata, &p.Metadata)
	_ = json.Unmarshal(paymentData, &p.PaymentData)
	_ = json.Unmarshal(route, &p.Route)
	if p.Route.Provider != event.Provider || p.Route.ProviderConnectionID != event.ProviderConnectionID || p.ProviderPaymentID != event.ProviderPaymentID {
		return core.EventResult{}, core.ErrConflict
	}
	next, ok := core.PublicStatusFromProvider(event.Data.Status)
	if !ok {
		return core.EventResult{}, core.ErrConflict
	}
	if next == p.Status || !core.PaymentTransitionAllowed(p.Status, next) {
		if err := tx.Commit(ctx); err != nil {
			return core.EventResult{}, err
		}
		return core.EventResult{Status: p.Status}, nil
	}
	previous := p.Status
	p.Status = next
	p.Version++
	_, err = tx.Exec(ctx, `UPDATE dinapay_v2_payments SET status=$2,provider_reference=COALESCE(NULLIF($3,''),provider_reference),resource_version=$4,updated_at=now() WHERE transaction_id=$1`, p.TransactionID, p.Status, event.Data.ProviderReference, p.Version)
	if err != nil {
		return core.EventResult{}, err
	}
	if next == "confirmed" {
		_, err = tx.Exec(ctx, `INSERT INTO dinacore_balance_outbox(ref_type,ref_id,account_id,amount,currency) VALUES('cashin',$1,$2,$3,$4) ON CONFLICT(ref_type,ref_id) DO NOTHING`, p.TransactionID, p.AccountID, p.Amount, p.Currency)
		if err != nil {
			return core.EventResult{}, err
		}
	}
	webhookPayload, _ := json.Marshal(map[string]any{"eventId": merchantEventID, "eventType": "payment.status_changed", "apiVersion": "2", "merchantId": p.MerchantID, "creationDate": event.ObservedAt, "resourceVersion": p.Version, "previousStatus": previous, "data": map[string]any{"object": p}})
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,event_id,event_type,payload) SELECT id,$1,'payment.status_changed',$2 FROM webhooks WHERE api_version='2' AND webhook_secret IS NOT NULL AND webhook_secret<>'' AND (merchant_id=$3 OR (account_id=$4 AND merchant_id IS NULL)) ON CONFLICT(webhook_id,event_id) DO NOTHING`, merchantEventID, webhookPayload, p.MerchantID, p.AccountID)
	if err != nil {
		return core.EventResult{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE dinapay_v2_provider_events SET changed_state=true WHERE event_id=$1`, event.EventID)
	if err != nil {
		return core.EventResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.EventResult{}, err
	}
	return core.EventResult{Changed: true, Status: p.Status}, nil
}
