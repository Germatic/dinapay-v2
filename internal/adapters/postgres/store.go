package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func (s *Store) BeginCreate(ctx context.Context, merchantID, key, hash, transactionID, externalID string) (core.Payment, bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO dinapay_v2_idempotency
      (merchant_id,idempotency_key,request_hash,transaction_id,status,external_id)
      VALUES ($1,$2,$3,$4,'pending',$5)
      ON CONFLICT (merchant_id,idempotency_key) DO NOTHING`, merchantID, key, hash, transactionID, externalID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "dinapay_v2_idempotency_merchant_external_uidx" {
			return core.Payment{}, false, core.ErrExternalIDConflict
		}
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
	  (transaction_id,account_id,merchant_id,external_id,status,amount,currency,payment_method,description,creation_date,expiration_date,action_url,success_url,cancel_url,customer,metadata,payment_data,provider_payment_id,provider_reference,route_decision,resource_version)
	  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,NULLIF($13,''),NULLIF($14,''),$15,$16,$17,$18,NULLIF($19,''),$20,$21)`,
		p.TransactionID, p.AccountID, p.MerchantID, p.ExternalID, p.Status, p.Amount, p.Currency, p.PaymentMethod, p.Description, p.CreationDate, p.ExpirationDate, p.ActionURL, p.SuccessURL, p.CancelURL, customer, metadata, paymentData, p.ProviderPaymentID, p.ProviderReference, route, p.Version)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries (webhook_id,event_id,event_type,payload)
      SELECT id,$1,$2,$3 FROM webhooks
      WHERE api_version='2' AND webhook_secret IS NOT NULL AND webhook_secret<>''
        AND (merchant_id=$4 OR (account_id=$5 AND merchant_id IS NULL))
        AND (event_types IS NULL OR $2=ANY(event_types))
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
	p, err := s.getV2(ctx, accountID, merchantID, transactionID)
	if !errors.Is(err, core.ErrNotFound) {
		return p, err
	}
	return s.getLegacy(ctx, accountID, merchantID, transactionID)
}

func (s *Store) GetCheckoutPayment(ctx context.Context, transactionID string) (core.CheckoutPayment, error) {
	var value core.CheckoutPayment
	var paymentData []byte
	err := s.db.QueryRow(ctx, `SELECT transaction_id::text,status,amount,currency,expiration_date,payment_data,COALESCE(success_url,''),COALESCE(cancel_url,''),resource_version
      FROM dinapay_v2_payments WHERE transaction_id=$1`, transactionID).Scan(
		&value.TransactionID, &value.Status, &value.Amount, &value.Currency, &value.ExpirationDate, &paymentData, &value.SuccessURL, &value.CancelURL, &value.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, core.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	_ = json.Unmarshal(paymentData, &value.PaymentData)
	return value, nil
}

func (s *Store) getV2(ctx context.Context, accountID, merchantID, transactionID string) (core.Payment, error) {
	var p core.Payment
	var customer, payer, metadata, paymentData, pricing, route, failure, providerFailure []byte
	err := s.db.QueryRow(ctx, `SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,COALESCE(received_amount,''),currency,payment_method,COALESCE(description,''),creation_date,expiration_date,confirmation_date,action_url,COALESCE(success_url,''),COALESCE(cancel_url,''),customer,payer,metadata,payment_data,COALESCE(pricing,'{}'),provider_payment_id,COALESCE(provider_reference,''),route_decision,resource_version,failure,provider_failure
      FROM dinapay_v2_payments WHERE transaction_id=$1 AND (($2<>'' AND merchant_id=$2) OR ($2='' AND $3<>'' AND account_id=$3))`, transactionID, merchantID, accountID).Scan(
		&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.ReceivedAmount, &p.Currency, &p.PaymentMethod, &p.Description, &p.CreationDate, &p.ExpirationDate, &p.ConfirmationDate, &p.ActionURL, &p.SuccessURL, &p.CancelURL, &customer, &payer, &metadata, &paymentData, &pricing, &p.ProviderPaymentID, &p.ProviderReference, &route, &p.Version, &failure, &providerFailure)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, core.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(customer, &p.Customer)
	_ = json.Unmarshal(payer, &p.Payer)
	_ = json.Unmarshal(metadata, &p.Metadata)
	_ = json.Unmarshal(paymentData, &p.PaymentData)
	_ = json.Unmarshal(pricing, &p.Pricing)
	_ = json.Unmarshal(route, &p.Route)
	p.Failure = paymentFailure(failure)
	_ = json.Unmarshal(providerFailure, &p.ProviderFailure)
	p.Origin = "v2"
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
	var customer, payer, metadata, paymentData, pricing, route, failure, providerFailure []byte
	err = tx.QueryRow(ctx, `SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,COALESCE(received_amount,''),currency,payment_method,COALESCE(description,''),creation_date,expiration_date,confirmation_date,action_url,customer,payer,metadata,payment_data,COALESCE(pricing,'{}'),provider_payment_id,COALESCE(provider_reference,''),route_decision,resource_version,failure,provider_failure FROM dinapay_v2_payments WHERE transaction_id=$1 FOR UPDATE`, event.TransactionID).Scan(&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.ReceivedAmount, &p.Currency, &p.PaymentMethod, &p.Description, &p.CreationDate, &p.ExpirationDate, &p.ConfirmationDate, &p.ActionURL, &customer, &payer, &metadata, &paymentData, &pricing, &p.ProviderPaymentID, &p.ProviderReference, &route, &p.Version, &failure, &providerFailure)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.EventResult{}, core.ErrNotFound
	}
	if err != nil {
		return core.EventResult{}, err
	}
	_ = json.Unmarshal(customer, &p.Customer)
	_ = json.Unmarshal(payer, &p.Payer)
	_ = json.Unmarshal(metadata, &p.Metadata)
	_ = json.Unmarshal(paymentData, &p.PaymentData)
	_ = json.Unmarshal(pricing, &p.Pricing)
	_ = json.Unmarshal(route, &p.Route)
	p.Failure = paymentFailure(failure)
	_ = json.Unmarshal(providerFailure, &p.ProviderFailure)
	if p.Route.Provider != event.Provider || p.Route.ProviderConnectionID != event.ProviderConnectionID || p.ProviderPaymentID != event.ProviderPaymentID {
		return core.EventResult{}, core.ErrConflict
	}
	next, ok := core.PublicStatusFromProvider(event.Data.Status)
	if !ok {
		return core.EventResult{}, core.ErrConflict
	}
	mergedPayer := p.Payer
	if next == "confirmed" && len(event.Data.Payer) > 0 {
		mergedPayer = make(core.Payer, len(p.Payer)+len(event.Data.Payer))
		for key, value := range p.Payer {
			mergedPayer[key] = value
		}
		for key, value := range event.Data.Payer {
			mergedPayer[key] = value
		}
	}
	payerChanged := !reflect.DeepEqual(p.Payer, mergedPayer)
	if next == p.Status && !payerChanged {
		if err := tx.Commit(ctx); err != nil {
			return core.EventResult{}, err
		}
		return core.EventResult{Status: p.Status}, nil
	}
	if next == p.Status && payerChanged {
		p.Payer = mergedPayer
		p.Version++
		payer, _ = json.Marshal(p.Payer)
		_, err = tx.Exec(ctx, `UPDATE dinapay_v2_payments SET provider_reference=COALESCE(NULLIF($2,''),provider_reference),resource_version=$3,payer=$4,updated_at=now() WHERE transaction_id=$1`, p.TransactionID, event.Data.ProviderReference, p.Version, payer)
		if err != nil {
			return core.EventResult{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return core.EventResult{}, err
		}
		return core.EventResult{Status: p.Status}, nil
	}
	if next != p.Status && !core.PaymentTransitionAllowed(p.Status, next) {
		if err := tx.Commit(ctx); err != nil {
			return core.EventResult{}, err
		}
		return core.EventResult{Status: p.Status}, nil
	}
	previous := p.Status
	p.Status = next
	p.Version++
	if next == "confirmed" && p.ConfirmationDate == nil {
		now := time.Now().UTC()
		p.ConfirmationDate = &now
	}
	netAmount := p.Amount
	if next == "confirmed" {
		p.ReceivedAmount = event.Data.Amount
		if payerChanged {
			p.Payer = mergedPayer
			payer, _ = json.Marshal(p.Payer)
		}
		if p.ReceivedAmount == "" {
			p.ReceivedAmount = p.Amount
		}
		if event.Data.Currency != "" && event.Data.Currency != p.Currency {
			return core.EventResult{}, core.ErrConflict
		}
		var feeAmount string
		err = tx.QueryRow(ctx, `WITH configured_fee AS (
		  SELECT COALESCE(
		    (SELECT GREATEST(ROUND($4::numeric*f.fee_rate,8),f.fee_minimum)
		       FROM control_plane_account_fee_rules f
		      WHERE f.account_id=$1 AND f.currency=$2 AND f.operation_type='payin'
		        AND f.provider_code IN ($3,'') AND f.effective_from<=now()
		      ORDER BY (f.provider_code=$3) DESC,f.effective_from DESC LIMIT 1),
		    (SELECT GREATEST(ROUND($4::numeric*f.payin_fee_pct,8),f.payin_fee_min)
		       FROM account_fees f WHERE f.account_id=$1 AND f.currency=$2
		        AND f.effective_from<=now() ORDER BY f.effective_from DESC LIMIT 1),
		    0) fee
		) SELECT fee::text,GREATEST($4::numeric-fee,0)::text FROM configured_fee`, p.AccountID, p.Currency, p.Route.Provider, p.ReceivedAmount).Scan(&feeAmount, &netAmount)
		if err != nil {
			return core.EventResult{}, err
		}
		p.Pricing = map[string]any{"feeAmount": feeAmount, "platformFeeAmount": "0"}
		pricing, _ = json.Marshal(p.Pricing)
	}
	if next == "failed" || next == "expired" || next == "cancelled" {
		public, native := normalizePaymentEventFailure(event.Data, next)
		publicRaw, _ := json.Marshal(public)
		nativeRaw, _ := json.Marshal(native)
		_ = json.Unmarshal(publicRaw, &p.Failure)
		_ = json.Unmarshal(nativeRaw, &p.ProviderFailure)
		failure, providerFailure = publicRaw, nativeRaw
	}
	_, err = tx.Exec(ctx, `UPDATE dinapay_v2_payments SET status=$2,provider_reference=COALESCE(NULLIF($3,''),provider_reference),resource_version=$4,confirmation_date=COALESCE(confirmation_date,$5),received_amount=COALESCE(NULLIF($6,''),received_amount),pricing=COALESCE(NULLIF($7::jsonb,'{}'::jsonb),pricing),failure=COALESCE(NULLIF($8::jsonb,'null'::jsonb),failure),provider_failure=COALESCE(NULLIF($9::jsonb,'null'::jsonb),provider_failure),payer=COALESCE(NULLIF($10::jsonb,'{}'::jsonb),payer),updated_at=now() WHERE transaction_id=$1`, p.TransactionID, p.Status, event.Data.ProviderReference, p.Version, p.ConfirmationDate, p.ReceivedAmount, pricing, failure, providerFailure, payer)
	if err != nil {
		return core.EventResult{}, err
	}
	if next == "confirmed" {
		_, err = tx.Exec(ctx, `INSERT INTO dinacore_balance_outbox(ref_type,ref_id,account_id,amount,currency) VALUES('cashin',$1,$2,$3,$4) ON CONFLICT(ref_type,ref_id) DO NOTHING`, p.TransactionID, p.AccountID, netAmount, p.Currency)
		if err != nil {
			return core.EventResult{}, err
		}
		if feeAmount, _ := p.Pricing["feeAmount"].(string); feeAmount != "" && feeAmount != "0" {
			_, err = tx.Exec(ctx, `INSERT INTO fee_events_outbox(ref_type,ref_id,account_id,fee_amount,currency,provider_fee) VALUES('payin',$1,$2,$3::numeric,$4,0) ON CONFLICT(ref_type,ref_id) DO NOTHING`, p.TransactionID, p.AccountID, feeAmount, p.Currency)
			if err != nil {
				return core.EventResult{}, err
			}
		}
	}
	webhookPayload, _ := json.Marshal(map[string]any{"eventId": merchantEventID, "eventType": "payment.status_changed", "apiVersion": "2", "merchantId": p.MerchantID, "creationDate": event.ObservedAt, "resourceVersion": p.Version, "previousStatus": previous, "data": map[string]any{"object": p}})
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,event_id,event_type,payload) SELECT id,$1,'payment.status_changed',$2 FROM webhooks WHERE api_version='2' AND webhook_secret IS NOT NULL AND webhook_secret<>'' AND (merchant_id=$3 OR (account_id=$4 AND merchant_id IS NULL)) AND (event_types IS NULL OR 'payment.status_changed'=ANY(event_types)) ON CONFLICT(webhook_id,event_id) DO NOTHING`, merchantEventID, webhookPayload, p.MerchantID, p.AccountID)
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
	failureCode := ""
	if p.Failure != nil {
		failureCode, _ = p.Failure["code"].(string)
	}
	return core.EventResult{Changed: true, Status: p.Status, FailureCode: failureCode}, nil
}

func paymentFailure(raw []byte) map[string]any {
	var normalized contract.Failure
	if json.Unmarshal(raw, &normalized) != nil || !contract.ValidPayment(normalized) {
		return nil
	}
	encoded, _ := json.Marshal(normalized)
	var out map[string]any
	_ = json.Unmarshal(encoded, &out)
	return out
}

func normalizePaymentEventFailure(data core.ProviderEventData, status string) (contract.Failure, contract.ProviderFailure) {
	code := contract.PaymentUnknownError
	if status == "expired" {
		code = contract.PaymentExpired
	} else if status == "cancelled" {
		code = contract.PaymentPayerCancelled
	}
	public := contract.NewPayment(code)
	if data.Failure != nil && contract.ValidPayment(*data.Failure) {
		public = *data.Failure
	}
	provider := contract.ProviderFailure{}
	if data.ProviderFailure != nil {
		provider = *data.ProviderFailure
	} else if legacy, ok := data.ProviderData["failure"]; ok {
		provider.Details = map[string]any{"legacyFailure": legacy}
	}
	if data.Failure != nil && !contract.ValidPayment(*data.Failure) {
		if provider.Details == nil {
			provider.Details = map[string]any{}
		}
		provider.Details["invalidNormalizedFailure"] = data.Failure
	}
	return public, provider
}
