package dinacore

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxWorker struct {
	db     *pgxpool.Pool
	client *Client
}

func NewOutboxWorker(db *pgxpool.Pool, client *Client) *OutboxWorker {
	return &OutboxWorker{db: db, client: client}
}

func (w *OutboxWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		w.flush(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *OutboxWorker) flush(ctx context.Context) {
	rows, err := w.db.Query(ctx, `WITH claimed AS (
		SELECT b.id FROM dinacore_balance_outbox b
		JOIN dinapay_v2_payments p ON p.transaction_id::text=b.ref_id
		WHERE b.sent=false AND b.next_attempt_at<=now()
		ORDER BY b.created_at LIMIT 100 FOR UPDATE OF b SKIP LOCKED
	) UPDATE dinacore_balance_outbox b SET next_attempt_at=now()+interval '1 minute'
	FROM claimed WHERE b.id=claimed.id RETURNING b.id,b.ref_id,b.account_id,b.amount::text,b.currency`)
	if err != nil {
		slog.Error("v2 balance outbox claim failed", "error", err)
		return
	}
	defer rows.Close()
	type item struct{ id, refID, accountID, amount, currency string }
	var items []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.refID, &x.accountID, &x.amount, &x.currency); err != nil {
			return
		}
		items = append(items, x)
	}
	for _, x := range items {
		if err = w.client.CreditBalance(ctx, x.accountID, x.refID, x.amount, x.currency); err != nil {
			_, _ = w.db.Exec(ctx, `UPDATE dinacore_balance_outbox SET attempt_count=attempt_count+1,last_error=$2,next_attempt_at=now()+least(interval '1 hour',interval '10 seconds'*power(2,least(attempt_count,8))) WHERE id=$1`, x.id, err.Error())
			continue
		}
		_, _ = w.db.Exec(ctx, `UPDATE dinacore_balance_outbox SET sent=true,sent_at=now(),last_error=NULL WHERE id=$1`, x.id)
	}
}
