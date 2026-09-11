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
		WHERE p.id=$1 AND (($2<>'' AND p.merchant_id=$2) OR ($2='' AND $3<>'' AND p.account_id=$3))`, transactionID, merchantID, accountID)
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
	rows, err := s.db.Query(ctx, query, merchantID, accountID, cursorTime, cursor.ID, limit+1)
	if err != nil {
		return core.PaymentPage{}, err
	}
	defer rows.Close()
	items := make([]core.Payment, 0, limit+1)
	created := make([]time.Time, 0, limit+1)
	for rows.Next() {
		var p core.Payment
		var customer, metadata, paymentData []byte
		var origin string
		var creation time.Time
		var expiration *time.Time
		if err := rows.Scan(&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.Currency, &p.PaymentMethod, &p.Description, &creation, &expiration, &p.ActionURL, &customer, &metadata, &paymentData, &origin); err != nil {
			return core.PaymentPage{}, err
		}
		p.CreationDate, p.Origin = creation, origin
		if expiration != nil {
			p.ExpirationDate = *expiration
		}
		_ = json.Unmarshal(customer, &p.Customer)
		_ = json.Unmarshal(metadata, &p.Metadata)
		_ = json.Unmarshal(paymentData, &p.PaymentData)
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

const legacySelect = `SELECT p.id::text,COALESCE(p.account_id,''),p.merchant_id,COALESCE(p.external_id,''),p.status,p.amount,p.currency,
	CASE WHEN p.provider='binancepay' THEN 'crypto_payment' WHEN p.currency='BRL' AND COALESCE(p.qr_data,'')<>'' THEN 'qr' ELSE 'bank_transfer' END,
	COALESCE(p.description,''),p.created_at,p.expiration,COALESCE(p.action_url,''),p.customer,p.metadata,p.provider,COALESCE(p.qr_data,''),COALESCE(p.coinag_reference,'') FROM payments p `

type rowScanner interface{ Scan(...any) error }

func scanLegacy(row rowScanner) (core.Payment, time.Time, error) {
	var p core.Payment
	var customer, metadata []byte
	var provider, qr, reference string
	var expiration *time.Time
	err := row.Scan(&p.TransactionID, &p.AccountID, &p.MerchantID, &p.ExternalID, &p.Status, &p.Amount, &p.Currency, &p.PaymentMethod, &p.Description, &p.CreationDate, &expiration, &p.ActionURL, &customer, &metadata, &provider, &qr, &reference)
	if err != nil {
		return p, time.Time{}, err
	}
	if expiration != nil {
		p.ExpirationDate = *expiration
	}
	_ = json.Unmarshal(customer, &p.Customer)
	_ = json.Unmarshal(metadata, &p.Metadata)
	p.PaymentData = legacyPaymentData(provider, p.ActionURL, qr, reference)
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
	SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,currency,payment_method,COALESCE(description,''),creation_date,expiration_date,action_url,customer,metadata,payment_data,'v2'::text origin FROM dinapay_v2_payments
	UNION ALL
	SELECT p.id::text,COALESCE(p.account_id,''),p.merchant_id,COALESCE(p.external_id,''),p.status,p.amount,p.currency,
	CASE WHEN p.provider='binancepay' THEN 'crypto_payment' WHEN p.currency='BRL' AND COALESCE(p.qr_data,'')<>'' THEN 'qr' ELSE 'bank_transfer' END,
	COALESCE(p.description,''),p.created_at,p.expiration,COALESCE(p.action_url,''),p.customer,p.metadata,
	CASE WHEN p.provider='binancepay' THEN jsonb_build_object('type','redirect','redirect',jsonb_strip_nulls(jsonb_build_object('recommendedAlternative','universal','links',jsonb_build_object('universal',p.action_url),'qr',CASE WHEN COALESCE(p.qr_data,'')<>'' THEN jsonb_build_object('content',p.qr_data) END))) ELSE jsonb_build_object('type','bank_transfer','bankTransfer',jsonb_strip_nulls(jsonb_build_object('transferReference',p.coinag_reference))) END,
	'v1'::text FROM payments p
) SELECT * FROM all_payments WHERE (($1<>'' AND merchant_id=$1) OR ($1='' AND $2<>'' AND account_id=$2))
	AND ($3::timestamptz IS NULL OR (creation_date,transaction_id)<($3::timestamptz,$4)) ORDER BY creation_date DESC,transaction_id DESC LIMIT $5`

const dashboardConsolidatedSelect = `WITH all_payments AS (
	SELECT transaction_id::text,account_id,merchant_id,external_id,status,amount,currency,payment_method,COALESCE(description,''),creation_date,expiration_date,action_url,customer,metadata,payment_data,'v2'::text origin FROM dinapay_v2_payments
	UNION ALL
	SELECT p.id::text,COALESCE(p.account_id,''),p.merchant_id,COALESCE(p.external_id,''),p.status,p.amount,p.currency,
	CASE WHEN p.provider='binancepay' THEN 'crypto_payment' WHEN p.currency='BRL' AND COALESCE(p.qr_data,'')<>'' THEN 'qr' ELSE 'bank_transfer' END,
	COALESCE(p.description,''),p.created_at,p.expiration,COALESCE(p.action_url,''),p.customer,p.metadata,
	CASE WHEN p.provider='binancepay' THEN jsonb_build_object('type','redirect','redirect',jsonb_strip_nulls(jsonb_build_object('recommendedAlternative','universal','links',jsonb_build_object('universal',p.action_url),'qr',CASE WHEN COALESCE(p.qr_data,'')<>'' THEN jsonb_build_object('content',p.qr_data) END))) ELSE jsonb_build_object('type','bank_transfer','bankTransfer',jsonb_strip_nulls(jsonb_build_object('transferReference',p.coinag_reference))) END,
	'v1'::text FROM payments p
) SELECT * FROM all_payments WHERE ($1='' OR merchant_id=$1) AND ($2='' OR account_id=$2)
	AND ($3::timestamptz IS NULL OR (creation_date,transaction_id)<($3::timestamptz,$4)) ORDER BY creation_date DESC,transaction_id DESC LIMIT $5`
