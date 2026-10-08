package reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"time"

	"github.com/Germatic/dinapay-v2/internal/adapters/dinacore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PayoutWorker struct {
	db     *pgxpool.Pool
	ledger interface {
		LedgerEntries(context.Context, string) ([]dinacore.LedgerEntry, error)
	}
	batch    int
	interval time.Duration
}

type payoutRecord struct {
	ID, AccountID, MerchantID, Status, Amount, Currency, TotalDebit string
	BalanceDebited                                                  bool
	ResourceVersion                                                 int64
}

type finding struct {
	RuleCode   string
	Severity   string
	Difference string
	Expected   map[string]any
	Observed   map[string]any
}

func NewPayoutWorker(db *pgxpool.Pool, ledger interface {
	LedgerEntries(context.Context, string) ([]dinacore.LedgerEntry, error)
}, batch int, interval time.Duration) *PayoutWorker {
	if batch < 1 || batch > 500 {
		batch = 50
	}
	if interval < time.Second {
		interval = 10 * time.Second
	}
	return &PayoutWorker{db: db, ledger: ledger, batch: batch, interval: interval}
}

func (w *PayoutWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		w.runBatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *PayoutWorker) runBatch(ctx context.Context) {
	ids, err := w.claim(ctx)
	if err != nil {
		slog.Error("payout reconciliation claim failed", "error", err)
		return
	}
	for _, id := range ids {
		if err = w.reconcile(ctx, id); err != nil {
			slog.Error("payout reconciliation failed", "payout_id", id, "error", err)
			_, _ = w.db.Exec(context.WithoutCancel(ctx), `UPDATE dinapay_reconciliation_queue SET next_check_at=now()+interval '1 minute',locked_until=NULL,last_error=left($2,2000),updated_at=now() WHERE domain='payout' AND operation_id=$1`, id, err.Error())
		}
	}
}

func (w *PayoutWorker) claim(ctx context.Context) ([]string, error) {
	rows, err := w.db.Query(ctx, `WITH claimed AS (
		SELECT domain,operation_id FROM dinapay_reconciliation_queue
		WHERE domain='payout' AND next_check_at<=now() AND (locked_until IS NULL OR locked_until<now())
		ORDER BY next_check_at,created_at LIMIT $1 FOR UPDATE SKIP LOCKED
	) UPDATE dinapay_reconciliation_queue q
	SET locked_until=now()+interval '1 minute',attempt_count=attempt_count+1,updated_at=now()
	FROM claimed WHERE q.domain=claimed.domain AND q.operation_id=claimed.operation_id
	RETURNING q.operation_id::text`, w.batch)
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
	return ids, rows.Err()
}

func (w *PayoutWorker) reconcile(ctx context.Context, id string) error {
	var p payoutRecord
	var pricing []byte
	err := w.db.QueryRow(ctx, `SELECT payout_id::text,account_id,merchant_id,status,source_amount,source_currency,pricing,balance_debited,resource_version FROM dinapay_v2_payouts WHERE payout_id=$1`, id).Scan(&p.ID, &p.AccountID, &p.MerchantID, &p.Status, &p.Amount, &p.Currency, &pricing, &p.BalanceDebited, &p.ResourceVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = w.db.Exec(ctx, `DELETE FROM dinapay_reconciliation_queue WHERE domain='payout' AND operation_id=$1 AND resource_version=$2`, id, p.ResourceVersion)
		return err
	}
	if err != nil {
		return err
	}
	var price map[string]any
	_ = json.Unmarshal(pricing, &price)
	p.TotalDebit, _ = price["totalDebitAmount"].(string)
	if p.TotalDebit == "" {
		p.TotalDebit = p.Amount
	}
	entries, err := w.ledger.LedgerEntries(ctx, id)
	if err != nil {
		return err
	}
	findings := evaluatePayout(p, entries)
	if err = w.persistFindings(ctx, p, findings); err != nil {
		return err
	}
	if len(findings) == 0 {
		_, err = w.db.Exec(ctx, `DELETE FROM dinapay_reconciliation_queue WHERE domain='payout' AND operation_id=$1`, id)
		return err
	}
	_, err = w.db.Exec(ctx, `UPDATE dinapay_reconciliation_queue SET next_check_at=now()+interval '5 minutes',locked_until=NULL,last_error=NULL,updated_at=now() WHERE domain='payout' AND operation_id=$1 AND resource_version=$2`, id, p.ResourceVersion)
	return err
}

func evaluatePayout(p payoutRecord, entries []dinacore.LedgerEntry) []finding {
	byType := make(map[string]dinacore.LedgerEntry, len(entries))
	observed := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		byType[entry.RefType] = entry
		observed = append(observed, map[string]any{"merchantId": entry.MerchantID, "currency": entry.Currency, "amount": entry.Amount, "refType": entry.RefType})
	}
	result := []finding{}
	if p.BalanceDebited {
		result = appendMismatch(result, "payout_debit", p, byType["payout"], negate(p.TotalDebit), observed)
	} else if entry, ok := byType["payout"]; ok {
		result = append(result, unexpected("payout_unexpected_debit", entry, observed))
	}

	expectedRelease := ""
	if p.BalanceDebited {
		switch p.Status {
		case "failed", "cancelled":
			expectedRelease = p.TotalDebit
		case "reversed":
			expectedRelease = p.Amount
		}
	}
	if expectedRelease != "" {
		result = appendMismatch(result, "payout_compensation", p, byType["payout_reservation_release"], expectedRelease, observed)
	} else if entry, ok := byType["payout_reservation_release"]; ok {
		result = append(result, unexpected("payout_unexpected_compensation", entry, observed))
	}
	return result
}

func appendMismatch(findings []finding, prefix string, p payoutRecord, entry dinacore.LedgerEntry, expectedAmount string, all []map[string]any) []finding {
	expected := map[string]any{"merchantId": p.AccountID, "currency": p.Currency, "amount": expectedAmount}
	if entry.ID == "" {
		return append(findings, finding{RuleCode: prefix + "_missing", Severity: "critical", Difference: absolute(expectedAmount), Expected: expected, Observed: map[string]any{"entries": all}})
	}
	if entry.MerchantID != p.AccountID || entry.Currency != p.Currency || !equalDecimal(entry.Amount, expectedAmount) {
		return append(findings, finding{RuleCode: prefix + "_mismatch", Severity: "critical", Difference: decimalDifference(entry.Amount, expectedAmount), Expected: expected, Observed: map[string]any{"merchantId": entry.MerchantID, "currency": entry.Currency, "amount": entry.Amount, "entries": all}})
	}
	return findings
}

func unexpected(code string, entry dinacore.LedgerEntry, all []map[string]any) finding {
	return finding{RuleCode: code, Severity: "critical", Difference: absolute(entry.Amount), Expected: map[string]any{"entry": nil}, Observed: map[string]any{"merchantId": entry.MerchantID, "currency": entry.Currency, "amount": entry.Amount, "entries": all}}
}

func (w *PayoutWorker) persistFindings(ctx context.Context, p payoutRecord, findings []finding) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	codes := make([]string, 0, len(findings))
	for _, item := range findings {
		codes = append(codes, item.RuleCode)
		expected, _ := json.Marshal(item.Expected)
		observed, _ := json.Marshal(item.Observed)
		_, err = tx.Exec(ctx, `INSERT INTO dinapay_reconciliation_findings(domain,operation_id,account_id,merchant_id,rule_code,severity,status,currency,difference,expected,observed)
			VALUES('payout',$1,$2,$3,$4,$5,'open',$6,$7::numeric,$8,$9)
			ON CONFLICT(domain,operation_id,rule_code) DO UPDATE SET account_id=excluded.account_id,merchant_id=excluded.merchant_id,severity=excluded.severity,status='open',currency=excluded.currency,difference=excluded.difference,expected=excluded.expected,observed=excluded.observed,occurrence_count=dinapay_reconciliation_findings.occurrence_count+1,last_seen_at=now(),resolved_at=NULL,updated_at=now()`, p.ID, p.AccountID, p.MerchantID, item.RuleCode, item.Severity, p.Currency, item.Difference, expected, observed)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE dinapay_reconciliation_findings SET status='resolved',resolved_at=now(),updated_at=now() WHERE domain='payout' AND operation_id=$1 AND status='open' AND NOT(rule_code=ANY($2::text[]))`, p.ID, codes)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func equalDecimal(left, right string) bool {
	l, lok := new(big.Rat).SetString(left)
	r, rok := new(big.Rat).SetString(right)
	return lok && rok && l.Cmp(r) == 0
}

func negate(value string) string {
	v, ok := new(big.Rat).SetString(value)
	if !ok {
		return "0"
	}
	v.Neg(v)
	return v.FloatString(8)
}

func absolute(value string) string {
	v, ok := new(big.Rat).SetString(value)
	if !ok {
		return "0"
	}
	if v.Sign() < 0 {
		v.Neg(v)
	}
	return v.FloatString(8)
}

func decimalDifference(observed, expected string) string {
	o, ook := new(big.Rat).SetString(observed)
	e, eok := new(big.Rat).SetString(expected)
	if !ook || !eok {
		return "0"
	}
	o.Sub(o, e)
	if o.Sign() < 0 {
		o.Neg(o)
	}
	return o.FloatString(8)
}
