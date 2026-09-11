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

func (s *Store) BeginPayout(ctx context.Context, principal core.Principal, key, hash, id string) (core.Payout, bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO dinapay_v2_payout_idempotency(merchant_id,idempotency_key,request_hash,payout_id,status) VALUES($1,$2,$3,$4,'pending') ON CONFLICT DO NOTHING`, principal.MerchantID, key, hash, id)
	if err != nil {
		return core.Payout{}, false, err
	}
	if tag.RowsAffected() == 1 {
		return core.Payout{}, false, nil
	}
	var storedHash, storedID, status string
	if err = s.db.QueryRow(ctx, `SELECT request_hash,payout_id::text,status FROM dinapay_v2_payout_idempotency WHERE merchant_id=$1 AND idempotency_key=$2`, principal.MerchantID, key).Scan(&storedHash, &storedID, &status); err != nil {
		return core.Payout{}, false, err
	}
	if storedHash != hash {
		return core.Payout{}, false, core.ErrConflict
	}
	if status != "complete" {
		return core.Payout{}, false, core.ErrInProgress
	}
	p, err := s.GetPayout(ctx, principal, storedID)
	return p, err == nil, err
}

func (s *Store) ListDashboardPayouts(ctx context.Context, accountID, merchantID string, o core.PayoutListOptions) (core.PayoutPage, error) {
	if o.Limit < 1 || o.Limit > 100 {
		o.Limit = 50
	}
	var cursor pageCursor
	if o.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(o.Cursor)
		if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == "" {
			return core.PayoutPage{}, fmt.Errorf("%w: invalid cursor", core.ErrInvalid)
		}
	}
	var cursorTime *time.Time
	if !cursor.CreatedAt.IsZero() {
		cursorTime = &cursor.CreatedAt
	}
	rows, err := s.db.Query(ctx, dashboardPayoutSelect, merchantID, accountID, o.ExternalID, o.Status, cursorTime, cursor.ID, o.Limit+1)
	if err != nil {
		return core.PayoutPage{}, err
	}
	defer rows.Close()
	out := core.PayoutPage{Data: []core.Payout{}}
	for rows.Next() {
		value, scanErr := scanPayout(rows)
		if scanErr != nil {
			return out, scanErr
		}
		out.Data = append(out.Data, value)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Data) > o.Limit {
		out.HasMore = true
		out.Data = out.Data[:o.Limit]
		last := out.Data[len(out.Data)-1]
		b, _ := json.Marshal(pageCursor{CreatedAt: last.CreationDate, ID: last.PayoutID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}

const dashboardPayoutSelect = `WITH all_payouts AS (
 SELECT payout_id::text,account_id,merchant_id,external_id,status,source_amount,source_currency,destination,pricing,remitter,COALESCE(description,''),metadata,COALESCE(provider_payout_id,''),COALESCE(provider_reference,''),COALESCE(provider_status,''),route_decision,balance_debited,provider_submitted,resource_version,next_attempt_at,creation_date,confirmation_date,failure_date,cancellation_date,reversal_date,COALESCE(last_error,'') FROM dinapay_v2_payouts
 UNION ALL
	SELECT p.id::text,COALESCE(p.account_id,''),COALESCE(p.merchant_id,''),COALESCE(p.external_id,''),p.status,p.amount::text,p.currency,
 COALESCE(p.destination,jsonb_build_object('country','AR','currency',COALESCE(NULLIF(p.destination_currency,''),p.currency),'amount',COALESCE(p.destination_amount::text,p.amount::text),'beneficiary',jsonb_build_object('name',COALESCE(p.destination_name,''),'documentNumber',COALESCE(p.destination_cuit,'')),'rail',jsonb_build_object('type',COALESCE(p.rail_code,'bank_transfer'),'identifier',COALESCE(p.destination_cbu,'')))),
 jsonb_strip_nulls(jsonb_build_object('feeAmount',p.fee_amount,'platformFeeAmount',p.platform_fee_amount,'exchangeRate',p.exchange_rate)), '{}'::jsonb, ''::text, '{}'::jsonb, '',COALESCE(p.provider_reference,p.coinag_trx_id,''),p.status,
 jsonb_build_object('provider',COALESCE(p.provider_code,''),'rail',COALESCE(p.rail_code,'')), p.status IN ('confirmed','completed'),p.submitted_at IS NOT NULL,1,p.created_at,p.created_at,p.completed_at,CASE WHEN p.status='failed' THEN p.updated_at END,NULL::timestamptz,p.reversed_at,COALESCE(p.error_message,'') FROM payouts p
) SELECT * FROM all_payouts WHERE ($1='' OR merchant_id=$1) AND ($2='' OR account_id=$2) AND ($3='' OR external_id=$3) AND ($4='' OR status=$4) AND ($5::timestamptz IS NULL OR (creation_date,payout_id)<($5::timestamptz,$6)) ORDER BY creation_date DESC,payout_id DESC LIMIT $7`

func (s *Store) ReleasePayout(ctx context.Context, merchantID, key string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM dinapay_v2_payout_idempotency WHERE merchant_id=$1 AND idempotency_key=$2 AND status='pending'`, merchantID, key)
	return err
}
func (s *Store) CompletePayout(ctx context.Context, principal core.Principal, key, id string, in core.CreatePayout, route core.RouteDecision) (core.Payout, error) {
	destination, _ := json.Marshal(in.Destination)
	remitter, _ := json.Marshal(in.Remitter)
	metadata, _ := json.Marshal(in.Metadata)
	routeJSON, _ := json.Marshal(route)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return core.Payout{}, err
	}
	defer tx.Rollback(ctx)
	inserted, err := tx.Exec(ctx, `INSERT INTO dinapay_v2_payouts(payout_id,account_id,merchant_id,external_id,idempotency_key,request_hash,status,source_amount,source_currency,destination,remitter,description,metadata,route_decision) SELECT $1,$2,$3,$4,$5,request_hash,'pending_debit',$6,$7,$8,$9,NULLIF($10,''),$11,$12 FROM dinapay_v2_payout_idempotency WHERE merchant_id=$3 AND idempotency_key=$5 AND payout_id=$1 AND status='pending'`, id, principal.AccountID, principal.MerchantID, in.ExternalID, key, in.Source.Amount, in.Source.Currency, destination, remitter, in.Description, metadata, routeJSON)
	if err != nil {
		return core.Payout{}, err
	}
	if inserted.RowsAffected() != 1 {
		return core.Payout{}, core.ErrConflict
	}
	p := core.Payout{PayoutID: id, AccountID: principal.AccountID, MerchantID: principal.MerchantID, ExternalID: in.ExternalID, Source: in.Source, Destination: in.Destination, Remitter: in.Remitter, Description: in.Description, Metadata: in.Metadata, Status: "processing", OperationalStatus: "pending_debit", Route: route, CreationDate: nowUTC(), ResourceVersion: 1}
	payload, _ := json.Marshal(map[string]any{"eventId": deterministicID("payout.created:" + id), "eventType": "payout.created", "apiVersion": "2", "merchantId": p.MerchantID, "creationDate": p.CreationDate, "resourceVersion": 1, "data": map[string]any{"object": p}})
	_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,event_id,event_type,payload) SELECT id,$1,'payout.created',$2 FROM webhooks WHERE api_version='2' AND webhook_secret<>'' AND (merchant_id=$3 OR (account_id=$4 AND merchant_id IS NULL)) ON CONFLICT DO NOTHING`, deterministicID("payout.created:"+id), payload, p.MerchantID, p.AccountID)
	if err != nil {
		return core.Payout{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE dinapay_v2_payout_idempotency SET status='complete',updated_at=now() WHERE merchant_id=$1 AND idempotency_key=$2 AND payout_id=$3 AND status='pending'`, principal.MerchantID, key, id)
	if err != nil {
		return core.Payout{}, err
	}
	if tag.RowsAffected() != 1 {
		return core.Payout{}, core.ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return core.Payout{}, err
	}
	return p, nil
}

const payoutSelect = `SELECT payout_id::text,account_id,merchant_id,external_id,status,source_amount,source_currency,destination,pricing,remitter,COALESCE(description,''),metadata,COALESCE(provider_payout_id,''),COALESCE(provider_reference,''),COALESCE(provider_status,''),route_decision,balance_debited,provider_submitted,resource_version,next_attempt_at,creation_date,confirmation_date,failure_date,cancellation_date,reversal_date,COALESCE(last_error,'') FROM dinapay_v2_payouts `

type payoutScanner interface{ Scan(...any) error }

func scanPayout(row payoutScanner) (core.Payout, error) {
	var p core.Payout
	var operational, providerStatus, lastError string
	var destination, pricing, remitter, metadata, route []byte
	err := row.Scan(&p.PayoutID, &p.AccountID, &p.MerchantID, &p.ExternalID, &operational, &p.Source.Amount, &p.Source.Currency, &destination, &pricing, &remitter, &p.Description, &metadata, &p.ProviderPayoutID, &p.BankSystemTrxID, &providerStatus, &route, &p.BalanceDebited, &p.ProviderSubmitted, &p.ResourceVersion, &p.NextAttemptAt, &p.CreationDate, &p.ConfirmationDate, &p.FailureDate, &p.CancellationDate, &p.ReversalDate, &lastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, core.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(destination, &p.Destination)
	_ = json.Unmarshal(pricing, &p.Pricing)
	_ = json.Unmarshal(remitter, &p.Remitter)
	_ = json.Unmarshal(metadata, &p.Metadata)
	_ = json.Unmarshal(route, &p.Route)
	p.OperationalStatus = operational
	p.Status = publicPayoutStatus(operational)
	if operational == "failed" && lastError != "" {
		p.Failure = map[string]any{"code": "payout_failed", "message": lastError, "retryable": false}
	}
	return p, nil
}
func publicPayoutStatus(v string) string {
	switch v {
	case "confirmed":
		return "confirmed"
	case "pending_compensation_reversed":
		return "confirmed"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "reversed":
		return "reversed"
	default:
		return "processing"
	}
}
func (s *Store) GetPayout(ctx context.Context, p core.Principal, id string) (core.Payout, error) {
	return scanPayout(s.db.QueryRow(ctx, payoutSelect+`WHERE payout_id=$1 AND (($2<>'' AND merchant_id=$2) OR ($2='' AND account_id=$3))`, id, p.MerchantID, p.AccountID))
}
func (s *Store) ListPayouts(ctx context.Context, p core.Principal, o core.PayoutListOptions) (core.PayoutPage, error) {
	if o.Limit < 1 || o.Limit > 100 {
		o.Limit = 50
	}
	rows, err := s.db.Query(ctx, payoutSelect+`WHERE (($1<>'' AND merchant_id=$1) OR ($1='' AND account_id=$2)) AND ($3='' OR external_id=$3) AND ($4='' OR status=$4) ORDER BY creation_date DESC,payout_id DESC LIMIT $5`, p.MerchantID, p.AccountID, o.ExternalID, o.Status, o.Limit+1)
	if err != nil {
		return core.PayoutPage{}, err
	}
	defer rows.Close()
	out := core.PayoutPage{Data: []core.Payout{}}
	for rows.Next() {
		v, e := scanPayout(rows)
		if e != nil {
			return out, e
		}
		out.Data = append(out.Data, v)
	}
	if len(out.Data) > o.Limit {
		out.HasMore = true
		out.Data = out.Data[:o.Limit]
		out.NextCursor = out.Data[len(out.Data)-1].PayoutID
	}
	return out, rows.Err()
}
func (s *Store) ClaimPayouts(ctx context.Context, limit int) ([]core.Payout, error) {
	rows, err := s.db.Query(ctx, `WITH claimed AS (SELECT payout_id FROM dinapay_v2_payouts WHERE status IN ('pending_debit','pending_provider','provider_unknown','processing','pending_compensation','pending_compensation_cancelled','pending_compensation_reversed') AND next_attempt_at<=now() ORDER BY creation_date LIMIT $1 FOR UPDATE SKIP LOCKED) UPDATE dinapay_v2_payouts p SET next_attempt_at=now()+interval '1 minute',attempt_count=attempt_count+1 FROM claimed WHERE p.payout_id=claimed.payout_id RETURNING p.payout_id::text`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	out := []core.Payout{}
	for _, id := range ids {
		p, e := scanPayout(s.db.QueryRow(ctx, payoutSelect+`WHERE payout_id=$1`, id))
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) TransitionPayout(ctx context.Context, id, next string, f map[string]any) (core.Payout, error) {
	expected, _ := f["expectedStatus"].(string)
	providerID, _ := f["providerPayoutId"].(string)
	reference, _ := f["providerReference"].(string)
	providerStatus, _ := f["providerStatus"].(string)
	lastError, _ := f["lastError"].(string)
	debited, _ := f["balanceDebited"].(bool)
	submitted, _ := f["providerSubmitted"].(bool)
	destinationAmount, _ := f["destinationAmount"].(string)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return core.Payout{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE dinapay_v2_payouts SET status=$3,provider_payout_id=COALESCE(NULLIF($4,''),provider_payout_id),provider_reference=COALESCE(NULLIF($5,''),provider_reference),provider_status=COALESCE(NULLIF($6,''),provider_status),last_error=NULLIF($7,''),balance_debited=balance_debited OR $8,provider_submitted=provider_submitted OR $9,destination=CASE WHEN $10='' THEN destination ELSE jsonb_set(destination,'{amount}',to_jsonb($10::text),true) END,resource_version=resource_version+1,next_attempt_at=now(),confirmation_date=CASE WHEN $3='confirmed' THEN now() ELSE confirmation_date END,failure_date=CASE WHEN $3='failed' THEN now() ELSE failure_date END,cancellation_date=CASE WHEN $3='cancelled' THEN now() ELSE cancellation_date END,reversal_date=CASE WHEN $3='reversed' THEN now() ELSE reversal_date END,updated_at=now() WHERE payout_id=$1 AND status=$2`, id, expected, next, providerID, reference, providerStatus, lastError, debited, submitted, destinationAmount)
	if err != nil {
		return core.Payout{}, err
	}
	if tag.RowsAffected() != 1 {
		return core.Payout{}, core.ErrConflict
	}
	p, err := scanPayout(tx.QueryRow(ctx, payoutSelect+`WHERE payout_id=$1`, id))
	if err != nil {
		return p, err
	}
	if publicPayoutStatus(expected) != p.Status {
		eventID := deterministicID("payout.status_changed:" + id + ":" + next)
		payload, _ := json.Marshal(map[string]any{"eventId": eventID, "eventType": "payout.status_changed", "apiVersion": "2", "merchantId": p.MerchantID, "creationDate": nowUTC(), "resourceVersion": p.ResourceVersion, "previousStatus": publicPayoutStatus(expected), "data": map[string]any{"object": p}})
		_, err = tx.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,event_id,event_type,payload) SELECT id,$1,'payout.status_changed',$2 FROM webhooks WHERE api_version='2' AND webhook_secret<>'' AND (merchant_id=$3 OR (account_id=$4 AND merchant_id IS NULL)) ON CONFLICT DO NOTHING`, eventID, payload, p.MerchantID, p.AccountID)
		if err != nil {
			return p, err
		}
	}
	return p, tx.Commit(ctx)
}

func (s *Store) ApplyPayoutProviderEvent(ctx context.Context, event core.ProviderEvent, merchantEventID string) (core.EventResult, error) {
	payload, _ := json.Marshal(event)
	tag, err := s.db.Exec(ctx, `INSERT INTO dinapay_v2_provider_events(event_id,event_type,payout_id,provider,source,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, event.EventID, event.EventType, event.PayoutID, event.Provider, event.Source, payload)
	if err != nil {
		return core.EventResult{}, err
	}
	if tag.RowsAffected() == 0 {
		return core.EventResult{Duplicate: true}, nil
	}
	p, err := scanPayout(s.db.QueryRow(ctx, payoutSelect+`WHERE payout_id=$1`, event.PayoutID))
	if err != nil {
		_, _ = s.db.Exec(context.WithoutCancel(ctx), `DELETE FROM dinapay_v2_provider_events WHERE event_id=$1`, event.EventID)
		return core.EventResult{}, err
	}
	if p.Route.Provider != event.Provider || p.Route.ProviderConnectionID != event.ProviderConnectionID || (p.ProviderPayoutID != "" && p.ProviderPayoutID != event.ProviderPayoutID) {
		_, _ = s.db.Exec(context.WithoutCancel(ctx), `DELETE FROM dinapay_v2_provider_events WHERE event_id=$1`, event.EventID)
		return core.EventResult{}, core.ErrConflict
	}
	next := "processing"
	switch event.Data.Status {
	case "confirmed":
		next = "confirmed"
	case "failed", "rejected":
		next = "pending_compensation"
	case "cancelled":
		next = "pending_compensation_cancelled"
	case "reversed":
		next = "pending_compensation_reversed"
	}
	if !payoutOperationalTransitionAllowed(p.OperationalStatus, next) {
		return core.EventResult{Status: p.Status}, nil
	}
	if next == p.OperationalStatus {
		return core.EventResult{Status: p.Status}, nil
	}
	f := map[string]any{"expectedStatus": p.OperationalStatus, "providerSubmitted": true, "providerPayoutId": event.ProviderPayoutID, "providerReference": event.Data.ProviderReference, "providerStatus": event.Data.RawStatus}
	updated, err := s.TransitionPayout(ctx, p.PayoutID, next, f)
	if err != nil {
		_, _ = s.db.Exec(context.WithoutCancel(ctx), `DELETE FROM dinapay_v2_provider_events WHERE event_id=$1`, event.EventID)
		return core.EventResult{}, err
	}
	_, _ = s.db.Exec(ctx, `UPDATE dinapay_v2_provider_events SET changed_state=true WHERE event_id=$1`, event.EventID)
	_ = merchantEventID
	return core.EventResult{Changed: updated.Status != p.Status, Status: updated.Status}, nil
}

func (s *Store) ResolvePayoutProviderEvent(ctx context.Context, event core.ProviderEvent) (core.ProviderEvent, error) {
	document, _ := event.Data.ProviderData["beneficiaryDocument"].(string)
	amount := event.Data.Amount
	if amount == "" {
		amount, _ = event.Data.ProviderData["sourceAmount"].(string)
	}
	if document == "" || amount == "" {
		return event, core.ErrNotFound
	}
	rows, err := s.db.Query(ctx, `SELECT payout_id::text FROM dinapay_v2_payouts WHERE status='provider_unknown' AND route_decision->>'provider'=$1 AND route_decision->>'providerConnectionId'=$2 AND UPPER(REPLACE(destination->'beneficiary'->>'documentNumber','-',''))=UPPER(REPLACE($3,'-','')) AND source_amount::numeric=$4::numeric AND creation_date>=now()-interval '48 hours' ORDER BY creation_date DESC LIMIT 2`, event.Provider, event.ProviderConnectionID, document, amount)
	if err != nil {
		return event, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return event, err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return event, core.ErrNotFound
	}
	if len(ids) > 1 {
		return event, core.ErrConflict
	}
	event.PayoutID = ids[0]
	return event, nil
}

func payoutOperationalTransitionAllowed(from, to string) bool {
	if from == to {
		return false
	}
	switch from {
	case "pending_debit":
		return to == "pending_provider" || to == "failed"
	case "pending_provider", "provider_unknown", "processing":
		return to == "processing" || to == "confirmed" || to == "pending_compensation" || to == "pending_compensation_cancelled"
	case "confirmed":
		return to == "pending_compensation_reversed"
	case "pending_compensation":
		return to == "failed"
	case "pending_compensation_cancelled":
		return to == "cancelled"
	case "pending_compensation_reversed":
		return to == "reversed"
	default:
		return false
	}
}
