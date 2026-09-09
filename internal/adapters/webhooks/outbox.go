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
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	db          *pgxpool.Pool
	client      *http.Client
	concurrency int
}
type delivery struct {
	id, webhookID, url, secret, eventID string
	payload                             []byte
	attempts                            int
	createdAt                           time.Time
}

func NewWorker(db *pgxpool.Pool, configuredConcurrency ...int) *Worker {
	concurrency := 8
	if len(configuredConcurrency) > 0 && configuredConcurrency[0] > 0 {
		concurrency = configuredConcurrency[0]
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 8
	transport.MaxConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 8 * time.Second
	return &Worker{db: db, client: &http.Client{Timeout: 10 * time.Second, Transport: transport}, concurrency: concurrency}
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
	RETURNING wd.id,wd.webhook_id,wh.webhook_url,wh.webhook_secret,wd.event_id,wd.payload,wd.attempt_count,wd.created_at`)
	if err != nil {
		slog.Error("v2 webhook outbox claim failed", "error", err)
		return
	}
	var batch []delivery
	for rows.Next() {
		var d delivery
		if err = rows.Scan(&d.id, &d.webhookID, &d.url, &d.secret, &d.eventID, &d.payload, &d.attempts, &d.createdAt); err != nil {
			rows.Close()
			return
		}
		batch = append(batch, d)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return
	}
	rows.Close()
	groups := groupByWebhook(batch)
	jobs := make(chan []delivery)
	var workers sync.WaitGroup
	for range min(w.concurrency, len(groups)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for group := range jobs {
				for _, d := range group {
					if !w.deliver(ctx, d) {
						break
					}
				}
			}
		}()
	}
	for _, group := range groups {
		select {
		case jobs <- group:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		}
	}
	close(jobs)
	workers.Wait()
}

func groupByWebhook(batch []delivery) [][]delivery {
	byID := make(map[string][]delivery)
	var ids []string
	for _, item := range batch {
		if _, exists := byID[item.webhookID]; !exists {
			ids = append(ids, item.webhookID)
		}
		byID[item.webhookID] = append(byID[item.webhookID], item)
	}
	sort.Strings(ids)
	groups := make([][]delivery, 0, len(ids))
	for _, id := range ids {
		group := byID[id]
		sort.SliceStable(group, func(i, j int) bool { return group[i].createdAt.Before(group[j].createdAt) })
		groups = append(groups, group)
	}
	return groups
}
func (w *Worker) deliver(ctx context.Context, d delivery) bool {
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
		return true
	}
	status := "pending"
	if d.attempts+1 >= 8 {
		status = "dead"
	}
	_, _ = w.db.Exec(ctx, `UPDATE webhook_deliveries SET status=$2,attempt_count=attempt_count+1,last_error=$3,next_attempt_at=now()+least(interval '1 hour',interval '10 seconds'*power(2,least(attempt_count,8))) WHERE id=$1`, d.id, status, err.Error())
	return false
}
func signature(secret string, body []byte, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, body)))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}
