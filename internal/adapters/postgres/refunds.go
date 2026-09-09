package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateRefund(ctx context.Context, principal core.Principal, paymentID, key string, in core.CreateRefund) (core.Refund, bool, error) {
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return core.Refund{}, false, err
	}
	defer tx.Rollback(ctx)
	var p core.Payment
	var route []byte
	err = tx.QueryRow(ctx, `SELECT transaction_id::text,account_id,merchant_id,status,amount,currency,provider_payment_id,route_decision FROM dinapay_v2_payments WHERE transaction_id=$1 AND (($2<>'' AND merchant_id=$2) OR ($2='' AND account_id=$3)) FOR UPDATE`, paymentID, principal.MerchantID, principal.AccountID).Scan(&p.TransactionID, &p.AccountID, &p.MerchantID, &p.Status, &p.Amount, &p.Currency, &p.ProviderPaymentID, &route)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Refund{}, false, core.ErrNotFound
	}
	if err != nil {
		return core.Refund{}, false, err
	}
	_ = json.Unmarshal(route, &p.Route)
	var existingID, existingHash string
	err = tx.QueryRow(ctx, `SELECT refund_id::text,request_hash FROM dinapay_v2_refunds WHERE merchant_id=$1 AND idempotency_key=$2`, p.MerchantID, key).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != hash {
			return core.Refund{}, false, core.ErrConflict
		}
		_ = tx.Commit(ctx)
		r, e := s.GetRefund(ctx, principal, existingID)
		return r, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return core.Refund{}, false, err
	}
	if p.Status != "confirmed" && p.Status != "refunded" {
		return core.Refund{}, false, fmt.Errorf("%w: payment is not refundable", core.ErrInvalid)
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT $1::numeric>0 AND $1::numeric<=($2::numeric-COALESCE((SELECT SUM(amount::numeric) FROM dinapay_v2_refunds WHERE transaction_id=$3 AND status<>'failed'),0))`, in.Amount, p.Amount, paymentID).Scan(&valid)
	if err != nil || !valid {
		return core.Refund{}, false, fmt.Errorf("%w: amount exceeds refundable balance", core.ErrInvalid)
	}
	id := deterministicID(p.MerchantID + ":refund:" + key)
	metadata, _ := json.Marshal(in.Metadata)
	_, err = tx.Exec(ctx, `INSERT INTO dinapay_v2_refunds(refund_id,transaction_id,account_id,merchant_id,external_id,idempotency_key,request_hash,status,amount,currency,reason,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,'pending_debit',$8,$9,NULLIF($10,''),$11)`, id, paymentID, p.AccountID, p.MerchantID, in.ExternalID, key, hash, in.Amount, p.Currency, in.Reason, metadata)
	if err != nil {
		return core.Refund{}, false, err
	}
	r := core.Refund{RefundID: id, TransactionID: paymentID, AccountID: p.AccountID, MerchantID: p.MerchantID, ExternalID: in.ExternalID, Status: "pending", Amount: in.Amount, Currency: p.Currency, Reason: in.Reason, Metadata: in.Metadata, CreationDate: nowUTC(), ProviderPaymentID: p.ProviderPaymentID, Route: p.Route, ResourceVersion: 1}
	payload, _ := json.Marshal(map[string]any{"eventId": deterministicID("refund.created:" + id), "eventType": "refund.created", "apiVersion": "2", "merchantId": p.MerchantID, "creationDate": r.CreationDate, "resourceVersion": 1, "data": map[string]any{"object": r}})
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,event_id,event_type,payload) SELECT id,$1,'refund.created',$2 FROM webhooks WHERE api_version='2' AND webhook_secret<>'' AND (merchant_id=$3 OR (account_id=$4 AND merchant_id IS NULL)) ON CONFLICT DO NOTHING`, deterministicID("refund.created:"+id), payload, p.MerchantID, p.AccountID)
	if err != nil {
		return core.Refund{}, false, err
	}
	return r, false, tx.Commit(ctx)
}

func deterministicID(v string) string {
	h := sha256.Sum256([]byte(v))
	b := append([]byte(nil), h[:16]...)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func nowUTC() time.Time { return time.Now().UTC() }

const refundSelect = `SELECT r.refund_id::text,r.transaction_id::text,r.account_id,r.merchant_id,r.external_id,r.status,r.amount,r.currency,COALESCE(r.reason,''),r.metadata,COALESCE(r.provider_refund_id,''),COALESCE(r.provider_status,''),r.balance_debited,r.provider_submitted,r.resource_version,r.next_attempt_at,r.creation_date,r.completion_date,p.provider_payment_id,p.route_decision,COALESCE(r.last_error,'') FROM dinapay_v2_refunds r JOIN dinapay_v2_payments p ON p.transaction_id=r.transaction_id `

type refundScanner interface{ Scan(...any) error }

func scanRefund(row refundScanner) (core.Refund, string, error) {
	var r core.Refund
	var metadata, route []byte
	var operational, lastError string
	err := row.Scan(&r.RefundID, &r.TransactionID, &r.AccountID, &r.MerchantID, &r.ExternalID, &operational, &r.Amount, &r.Currency, &r.Reason, &metadata, &r.ProviderReference, &r.Status, &r.BalanceDebited, &r.ProviderSubmitted, &r.ResourceVersion, &r.NextAttemptAt, &r.CreationDate, &r.CompletionDate, &r.ProviderPaymentID, &route, &lastError)
	if err != nil {
		return r, "", err
	}
	r.OperationalStatus = operational
	r.Status = publicRefundStatus(operational)
	if lastError != "" {
		r.Failure = map[string]any{"code": "refund_failed", "message": lastError}
	}
	_ = json.Unmarshal(metadata, &r.Metadata)
	_ = json.Unmarshal(route, &r.Route)
	return r, operational, nil
}
func publicRefundStatus(v string) string {
	if v == "succeeded" {
		return "succeeded"
	}
	if v == "failed" {
		return "failed"
	}
	return "pending"
}

func (s *Store) GetRefund(ctx context.Context, p core.Principal, id string) (core.Refund, error) {
	r, _, err := scanRefund(s.db.QueryRow(ctx, refundSelect+` WHERE r.refund_id=$1 AND (($2<>'' AND r.merchant_id=$2) OR ($2='' AND r.account_id=$3))`, id, p.MerchantID, p.AccountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, core.ErrNotFound
	}
	return r, err
}
func (s *Store) ListRefunds(ctx context.Context, p core.Principal, paymentID string) (core.RefundList, error) {
	rows, err := s.db.Query(ctx, refundSelect+` WHERE r.transaction_id=$1 AND (($2<>'' AND r.merchant_id=$2) OR ($2='' AND r.account_id=$3)) ORDER BY r.creation_date`, paymentID, p.MerchantID, p.AccountID)
	if err != nil {
		return core.RefundList{}, err
	}
	defer rows.Close()
	out := core.RefundList{Data: []core.Refund{}}
	for rows.Next() {
		r, _, e := scanRefund(rows)
		if e != nil {
			return out, e
		}
		out.Data = append(out.Data, r)
	}
	return out, rows.Err()
}
func (s *Store) ClaimRefunds(ctx context.Context, limit int) ([]core.Refund, error) {
	rows, err := s.db.Query(ctx, `WITH claimed AS (SELECT refund_id FROM dinapay_v2_refunds WHERE status IN ('pending_debit','pending_provider','pending','pending_compensation') AND next_attempt_at<=now() ORDER BY creation_date LIMIT $1 FOR UPDATE SKIP LOCKED) UPDATE dinapay_v2_refunds r SET next_attempt_at=now()+interval '1 minute',attempt_count=attempt_count+1 FROM claimed WHERE r.refund_id=claimed.refund_id RETURNING r.refund_id::text`, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []core.Refund
	for _, id := range ids {
		r, _, e := scanRefund(s.db.QueryRow(ctx, refundSelect+` WHERE r.refund_id=$1`, id))
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Store) TransitionRefund(ctx context.Context, id, next string, fields map[string]any) (core.Refund, error) {
	expected, _ := fields["expectedStatus"].(string)
	providerID, _ := fields["providerRefundId"].(string)
	providerStatus, _ := fields["providerStatus"].(string)
	lastError, _ := fields["lastError"].(string)
	debited, _ := fields["balanceDebited"].(bool)
	submitted, _ := fields["providerSubmitted"].(bool)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return core.Refund{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE dinapay_v2_refunds SET status=$3,provider_refund_id=COALESCE(NULLIF($4,''),provider_refund_id),provider_status=COALESCE(NULLIF($5,''),provider_status),last_error=NULLIF($6,''),balance_debited=balance_debited OR $7,provider_submitted=provider_submitted OR $8,resource_version=resource_version+1,next_attempt_at=now(),completion_date=CASE WHEN $3 IN ('succeeded','failed') THEN now() ELSE completion_date END,updated_at=now() WHERE refund_id=$1 AND status=$2`, id, expected, next, providerID, providerStatus, lastError, debited, submitted)
	if err != nil {
		return core.Refund{}, err
	}
	if tag.RowsAffected() != 1 {
		return core.Refund{}, core.ErrConflict
	}
	r, _, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE r.refund_id=$1`, id))
	if err != nil {
		return r, err
	}
	if next == "succeeded" {
		_, err = tx.Exec(ctx, `UPDATE dinapay_v2_payments p SET status=CASE WHEN (SELECT COALESCE(SUM(amount::numeric),0) FROM dinapay_v2_refunds WHERE transaction_id=p.transaction_id AND status='succeeded')>=p.amount::numeric THEN 'refunded' ELSE p.status END,updated_at=now() WHERE transaction_id=$1`, r.TransactionID)
		if err != nil {
			return r, err
		}
	}
	if next == "succeeded" || next == "failed" {
		eventID := deterministicID("refund.status_changed:" + id + ":" + next)
		payload, _ := json.Marshal(map[string]any{"eventId": eventID, "eventType": "refund.status_changed", "apiVersion": "2", "merchantId": r.MerchantID, "creationDate": nowUTC(), "resourceVersion": r.ResourceVersion, "previousStatus": "pending", "data": map[string]any{"object": r}})
		_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,event_id,event_type,payload) SELECT id,$1,'refund.status_changed',$2 FROM webhooks WHERE api_version='2' AND webhook_secret<>'' AND (merchant_id=$3 OR (account_id=$4 AND merchant_id IS NULL)) ON CONFLICT DO NOTHING`, eventID, payload, r.MerchantID, r.AccountID)
		if err != nil {
			return r, err
		}
	}
	return r, tx.Commit(ctx)
}
