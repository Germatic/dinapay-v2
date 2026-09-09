package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	db     *pgxpool.Pool
	client *http.Client
}
type delivery struct {
	id, url, secret, eventID string
	payload                  []byte
	attempts                 int
}

func NewWorker(db *pgxpool.Pool) *Worker {
	return &Worker{db: db, client: &http.Client{Timeout: 10 * time.Second}}
}
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
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
func (w *Worker) flush(ctx context.Context) {
	rows, err := w.db.Query(ctx, `WITH claimed AS (
		SELECT wd.id FROM webhook_deliveries wd JOIN webhooks wh ON wh.id=wd.webhook_id
		WHERE wd.status='pending' AND wd.next_attempt_at<=now() AND wh.api_version='2'
		  AND wd.payload->>'apiVersion'='2'
		ORDER BY wd.next_attempt_at LIMIT 50 FOR UPDATE OF wd SKIP LOCKED
	) UPDATE webhook_deliveries wd SET next_attempt_at=now()+interval '1 minute'
	FROM claimed,webhooks wh WHERE wd.id=claimed.id AND wh.id=wd.webhook_id
	RETURNING wd.id,wh.webhook_url,wh.webhook_secret,wd.event_id,wd.payload,wd.attempt_count`)
	if err != nil {
		slog.Error("v2 webhook outbox claim failed", "error", err)
		return
	}
	defer rows.Close()
	var batch []delivery
	for rows.Next() {
		var d delivery
		if err = rows.Scan(&d.id, &d.url, &d.secret, &d.eventID, &d.payload, &d.attempts); err != nil {
			return
		}
		batch = append(batch, d)
	}
	for _, d := range batch {
		w.deliver(ctx, d)
	}
}
func (w *Worker) deliver(ctx context.Context, d delivery) {
	timestamp := time.Now().UTC().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(d.payload))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Timestamp", fmt.Sprint(timestamp))
		req.Header.Set("X-Webhook-Signature", signature(d.secret, d.payload, timestamp))
		var response *http.Response
		response, err = w.client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = fmt.Errorf("http %d", response.StatusCode)
			}
		}
	}
	if err == nil {
		_, _ = w.db.Exec(ctx, `UPDATE webhook_deliveries SET status='delivered',attempt_count=attempt_count+1,delivered_at=now(),last_error=NULL WHERE id=$1`, d.id)
		return
	}
	status := "pending"
	if d.attempts+1 >= 8 {
		status = "dead"
	}
	_, _ = w.db.Exec(ctx, `UPDATE webhook_deliveries SET status=$2,attempt_count=attempt_count+1,last_error=$3,next_attempt_at=now()+least(interval '1 hour',interval '10 seconds'*power(2,least(attempt_count,8))) WHERE id=$1`, d.id, status, err.Error())
}
func signature(secret string, body []byte, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, body)))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}
