package postgres

import (
	"context"
	"errors"
	"strconv"

	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/jackc/pgx/v5"
)

func webhookScope(p core.Principal) (string, string) {
	if p.MerchantID != "" {
		return "merchant", p.MerchantID
	}
	return "account", p.AccountID
}

func webhookOwnerSQL(p core.Principal, offset int) (string, []any) {
	if p.MerchantID != "" {
		return "merchant_id=$" + strconv.Itoa(offset), []any{p.MerchantID}
	}
	return "account_id=$" + strconv.Itoa(offset) + " AND merchant_id IS NULL", []any{p.AccountID}
}

func scanWebhook(row interface{ Scan(...any) error }) (core.WebhookSubscription, error) {
	var w core.WebhookSubscription
	var merchantID, accountID *string
	err := row.Scan(&w.WebhookID, &w.URL, &merchantID, &accountID, &w.EventTypes, &w.CreationDate, &w.UpdatedDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, core.ErrNotFound
	}
	if err != nil {
		return w, err
	}
	w.APIVersion = "2"
	if merchantID != nil {
		w.Scope = "merchant"
	} else {
		w.Scope = "account"
	}
	return w, nil
}

const webhookReturning = ` RETURNING id::text,webhook_url,merchant_id,account_id,event_types,created_at,COALESCE(updated_at,created_at)`

func (s *Store) CreateWebhook(ctx context.Context, p core.Principal, url, secret string, events []string) (core.WebhookSubscription, error) {
	scope, owner := webhookScope(p)
	var row pgx.Row
	if scope == "merchant" {
		row = s.db.QueryRow(ctx, `INSERT INTO webhooks(webhook_url,merchant_id,webhook_secret,api_version,event_types,created_at,updated_at) VALUES($1,$2,$3,'2',$4,now(),now()) ON CONFLICT DO NOTHING`+webhookReturning, url, owner, secret, nullableEvents(events))
	} else {
		row = s.db.QueryRow(ctx, `INSERT INTO webhooks(webhook_url,account_id,webhook_secret,api_version,event_types,created_at,updated_at) VALUES($1,$2,$3,'2',$4,now(),now()) ON CONFLICT DO NOTHING`+webhookReturning, url, owner, secret, nullableEvents(events))
	}
	w, err := scanWebhook(row)
	if errors.Is(err, core.ErrNotFound) {
		return w, core.ErrConflict
	}
	if err == nil {
		w.Secret = secret
	}
	return w, err
}

func nullableEvents(events []string) any {
	if len(events) == 0 {
		return nil
	}
	return events
}

func (s *Store) ListWebhooks(ctx context.Context, p core.Principal) ([]core.WebhookSubscription, error) {
	owner, args := webhookOwnerSQL(p, 1)
	rows, err := s.db.Query(ctx, `SELECT id::text,webhook_url,merchant_id,account_id,event_types,created_at,COALESCE(updated_at,created_at) FROM webhooks WHERE api_version='2' AND `+owner+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.WebhookSubscription{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) UpdateWebhook(ctx context.Context, p core.Principal, id string, url *string, events *[]string) (core.WebhookSubscription, error) {
	owner, ownerArgs := webhookOwnerSQL(p, 5)
	var eventValue any
	if events != nil {
		eventValue = nullableEvents(*events)
	}
	args := []any{id, url, events != nil, eventValue}
	args = append(args, ownerArgs...)
	return scanWebhook(s.db.QueryRow(ctx, `UPDATE webhooks SET webhook_url=COALESCE($2,webhook_url),event_types=CASE WHEN $3 THEN $4 ELSE event_types END,updated_at=now() WHERE id=$1 AND api_version='2' AND `+owner+webhookReturning, args...))
}

func (s *Store) DeleteWebhook(ctx context.Context, p core.Principal, id string) error {
	owner, ownerArgs := webhookOwnerSQL(p, 2)
	args := []any{id}
	args = append(args, ownerArgs...)
	tag, err := s.db.Exec(ctx, `DELETE FROM webhooks WHERE id=$1 AND api_version='2' AND `+owner, args...)
	if err == nil && tag.RowsAffected() == 0 {
		return core.ErrNotFound
	}
	return err
}

func (s *Store) RotateWebhookSecret(ctx context.Context, p core.Principal, id, secret string) (core.WebhookSubscription, error) {
	owner, ownerArgs := webhookOwnerSQL(p, 3)
	args := []any{id, secret}
	args = append(args, ownerArgs...)
	w, err := scanWebhook(s.db.QueryRow(ctx, `UPDATE webhooks SET previous_secret=webhook_secret,previous_secret_expires_at=now()+interval '24 hours',webhook_secret=$2,updated_at=now() WHERE id=$1 AND api_version='2' AND `+owner+webhookReturning, args...))
	if err == nil {
		w.Secret = secret
	}
	return w, err
}
