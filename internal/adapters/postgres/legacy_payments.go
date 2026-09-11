package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/jackc/pgx/v5"
)

type pageCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

func (s *Store) getLegacy(ctx context.Context, accountID, merchantID, transactionID string) (core.Payment, error) {
	row := s.db.QueryRow(ctx, legacySelect+`
		WHERE p.id=$1 AND (($2<>'' AND p.merchant_id=$2) OR ($2='' AND $3<>'' AND COALESCE(NULLIF(p.account_id,''),m.account_id)=$3))`, transactionID, merchantID, accountID)
	p, _, err := scanLegacy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, core.ErrNotFound
	}
	return p, err
}

func (s *Store) List(ctx context.Context, accountID, merchantID string, options core.PaymentListOptions) (core.PaymentPage, error) {
	return s.listConsolidated(ctx, consolidatedSelect, accountID, merchantID, options)
}

func (s *Store) ListDashboardPayments(ctx context.Context, accountID, merchantID string, options core.PaymentListOptions) (core.PaymentPage, error) {
	return s.listConsolidated(ctx, dashboardConsolidatedSelect, accountID, merchantID, options)
}

func (s *Store) listConsolidated(ctx context.Context, query, accountID, merchantID string, options core.PaymentListOptions) (core.PaymentPage, error) {
	limit := options.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var cursor pageCursor
	if options.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(options.Cursor)
		if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == "" {
			return core.PaymentPage{}, fmt.Errorf("%w: invalid cursor", core.ErrInvalid)
		}
	}
	var cursorTime *time.Time
	if !cursor.CreatedAt.IsZero() {
		cursorTime = &cursor.CreatedAt
	}
	rows, err := s.db.Query(ctx, query, merchantID, accountID, options.Status, options.Currency, options.ExternalID, options.CreatedAfter, options.CreatedBefore, options.ConfirmedAfter, options.ConfirmedBefore, cursorTime, cursor.ID, limit+1)
	if err != nil {
		return core.PaymentPage{}, err
	}
	defer rows.Close()
	items := make([]core.Payment, 0, limit+1)
	created := make([]time.Time, 0, limit+1)
	for rows.Next() {
		var p core.Payment
		var customer, metadata, paymentData, pricing []byte
		var origin string
		var creation time.Time
		var expiration, confirmation *time.Time
		if err := rows.Scan(&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.ReceivedAmount, &p.Currency, &p.PaymentMethod, &p.Description, &creation, &expiration, &confirmation, &p.ActionURL, &customer, &metadata, &paymentData, &pricing, &origin); err != nil {
			return core.PaymentPage{}, err
		}
		p.CreationDate, p.Origin = creation, origin
		if expiration != nil {
			p.ExpirationDate = *expiration
		}
		p.ConfirmationDate = confirmation
		_ = json.Unmarshal(customer, &p.Customer)
		_ = json.Unmarshal(metadata, &p.Metadata)
		_ = json.Unmarshal(paymentData, &p.PaymentData)
		_ = json.Unmarshal(pricing, &p.Pricing)
		items, created = append(items, p), append(created, creation)
	}
	if err := rows.Err(); err != nil {
		return core.PaymentPage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items, created = items[:limit], created[:limit]
	}
	page := core.PaymentPage{Data: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		b, _ := json.Marshal(pageCursor{CreatedAt: created[len(created)-1], ID: items[len(items)-1].TransactionID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}

const legacySelect = `SELECT p.id::text,COALESCE(NULLIF(p.account_id,''),m.account_id,''),p.merchant_id,COALESCE(p.external_id,''),p.status,p.amount,COALESCE(p.received_amount,''),p.currency,
	CASE WHEN p.provider='binancepay' THEN 'crypto_payment' WHEN p.currency='BRL' AND COALESCE(p.qr_data,'')<>'' THEN 'qr' ELSE 'bank_transfer' END,
	COALESCE(p.description,''),p.created_at,p.expiration,p.received_at,COALESCE(p.action_url,''),p.customer,p.metadata,p.provider,COALESCE(p.qr_data,''),COALESCE(p.coinag_reference,''),jsonb_build_object('feeAmount',COALESCE(p.fee_amount,0)::text,'platformFeeAmount',COALESCE(p.platform_fee_amount,0)::text) FROM payments p LEFT JOIN merchants m ON m.id=p.merchant_id `

type rowScanner interface{ Scan(...any) error }

func scanLegacy(row rowScanner) (core.Payment, time.Time, error) {
	var p core.Payment
	var customer, metadata, pricing []byte
	var provider, qr, reference string
	var expiration, confirmation *time.Time
	err := row.Scan(&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.ReceivedAmount, &p.Currency, &p.PaymentMethod, &p.Description, &p.CreationDate, &expiration, &confirmation, &p.ActionURL, &customer, &metadata, &provider, &qr, &reference, &pricing)
	if err != nil {
		return p, time.Time{}, err
	}
	if expiration != nil {
		p.ExpirationDate = *expiration
	}
	p.ConfirmationDate = confirmation
	_ = json.Unmarshal(customer, &p.Customer)
	_ = json.Unmarshal(metadata, &p.Metadata)
	p.PaymentData = legacyPaymentData(provider, p.ActionURL, qr, reference)
	_ = json.Unmarshal(pricing, &p.Pricing)
	p.Origin = "v1"
	return p, p.CreationDate, nil
}

func legacyPaymentData(provider, actionURL, qr, reference string) map[string]any {
	if provider == "binancepay" {
		redirect := map[string]any{"recommendedAlternative": "universal", "links": map[string]any{"universal": actionURL}}
		if qr != "" {
			redirect["qr"] = map[string]any{"content": qr}
		}
		return map[string]any{"type": "redirect", "redirect": redirect}
	}
	bank := map[string]any{}
	if reference != "" {
		bank["transferReference"] = reference
	}
	return map[string]any{"type": "bank_transfer", "bankTransfer": bank}
}

const consolidatedSelect = `WITH all_payments AS (
	SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,COALESCE(received_amount,''),currency,payment_method,COALESCE(description,''),creation_date,expiration_date,confirmation_date,action_url,customer,metadata,payment_data,COALESCE(pricing,'{}'),'v2'::text origin FROM dinapay_v2_payments
	UNION ALL
	SELECT p.id::text,COALESCE(NULLIF(p.account_id,''),m.account_id,''),p.merchant_id,COALESCE(p.external_id,''),p.status,p.amount,COALESCE(p.received_amount,''),p.currency,
	CASE WHEN p.provider='binancepay' THEN 'crypto_payment' WHEN p.currency='BRL' AND COALESCE(p.qr_data,'')<>'' THEN 'qr' ELSE 'bank_transfer' END,
	COALESCE(p.description,''),p.created_at,p.expiration,p.received_at,COALESCE(p.action_url,''),p.customer,p.metadata,
	CASE WHEN p.provider='binancepay' THEN jsonb_build_object('type','redirect','redirect',jsonb_strip_nulls(jsonb_build_object('recommendedAlternative','universal','links',jsonb_build_object('universal',p.action_url),'qr',CASE WHEN COALESCE(p.qr_data,'')<>'' THEN jsonb_build_object('content',p.qr_data) END))) ELSE jsonb_build_object('type','bank_transfer','bankTransfer',jsonb_strip_nulls(jsonb_build_object('transferReference',p.coinag_reference))) END,jsonb_build_object('feeAmount',COALESCE(p.fee_amount,0)::text,'platformFeeAmount',COALESCE(p.platform_fee_amount,0)::text),
	'v1'::text FROM payments p LEFT JOIN merchants m ON m.id=p.merchant_id
) SELECT * FROM all_payments WHERE (($1<>'' AND merchant_id=$1) OR ($1='' AND $2<>'' AND account_id=$2))
	AND ($3='' OR status=$3) AND ($4='' OR currency=$4) AND ($5='' OR external_id=$5)
	AND ($6::timestamptz IS NULL OR creation_date >= $6) AND ($7::timestamptz IS NULL OR creation_date < $7)
	AND ($8::timestamptz IS NULL OR confirmation_date >= $8) AND ($9::timestamptz IS NULL OR confirmation_date < $9)
	AND ($10::timestamptz IS NULL OR (creation_date,transaction_id)<($10::timestamptz,$11)) ORDER BY creation_date DESC,transaction_id DESC LIMIT $12`

const dashboardConsolidatedSelect = `WITH all_payments AS (
	SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,COALESCE(received_amount,''),currency,payment_method,COALESCE(description,''),creation_date,expiration_date,confirmation_date,action_url,customer,metadata,payment_data,COALESCE(pricing,'{}'),'v2'::text origin FROM dinapay_v2_payments
	UNION ALL
	SELECT p.id::text,COALESCE(NULLIF(p.account_id,''),m.account_id,''),p.merchant_id,COALESCE(p.external_id,''),p.status,p.amount,COALESCE(p.received_amount,''),p.currency,
	CASE WHEN p.provider='binancepay' THEN 'crypto_payment' WHEN p.currency='BRL' AND COALESCE(p.qr_data,'')<>'' THEN 'qr' ELSE 'bank_transfer' END,
	COALESCE(p.description,''),p.created_at,p.expiration,p.received_at,COALESCE(p.action_url,''),p.customer,p.metadata,
	CASE WHEN p.provider='binancepay' THEN jsonb_build_object('type','redirect','redirect',jsonb_strip_nulls(jsonb_build_object('recommendedAlternative','universal','links',jsonb_build_object('universal',p.action_url),'qr',CASE WHEN COALESCE(p.qr_data,'')<>'' THEN jsonb_build_object('content',p.qr_data) END))) ELSE jsonb_build_object('type','bank_transfer','bankTransfer',jsonb_strip_nulls(jsonb_build_object('transferReference',p.coinag_reference))) END,jsonb_build_object('feeAmount',COALESCE(p.fee_amount,0)::text,'platformFeeAmount',COALESCE(p.platform_fee_amount,0)::text),
	'v1'::text FROM payments p LEFT JOIN merchants m ON m.id=p.merchant_id
) SELECT * FROM all_payments WHERE ($1='' OR merchant_id=$1) AND ($2='' OR account_id=$2)
	AND ($3='' OR status=$3) AND ($4='' OR currency=$4) AND ($5='' OR external_id=$5)
	AND ($6::timestamptz IS NULL OR creation_date >= $6) AND ($7::timestamptz IS NULL OR creation_date < $7)
	AND ($8::timestamptz IS NULL OR confirmation_date >= $8) AND ($9::timestamptz IS NULL OR confirmation_date < $9)
	AND ($10::timestamptz IS NULL OR (creation_date,transaction_id)<($10::timestamptz,$11)) ORDER BY creation_date DESC,transaction_id DESC LIMIT $12`
