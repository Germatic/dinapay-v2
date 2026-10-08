package postgres

import (
	"context"
	"encoding/json"
	"github.com/Germatic/dinapay-v2/internal/core"
)

func (s *Store) ListReconciliationFindings(ctx context.Context, options core.ReconciliationFindingOptions) (core.ReconciliationFindingPage, error) {
	rows, err := s.db.Query(ctx, `SELECT id,domain,operation_id::text,account_id,merchant_id,rule_code,severity,status,currency,difference::text,expected,observed,occurrence_count,first_seen_at,last_seen_at,resolved_at,count(*) OVER() FROM dinapay_reconciliation_findings WHERE ($1='' OR status=$1) AND ($2='' OR severity=$2) AND ($3='' OR domain=$3) AND ($4='' OR account_id=$4) AND ($5='' OR merchant_id=$5) ORDER BY CASE WHEN status='open' THEN 0 ELSE 1 END,CASE WHEN severity='critical' THEN 0 ELSE 1 END,last_seen_at DESC,id DESC LIMIT $6 OFFSET $7`, options.Status, options.Severity, options.Domain, options.AccountID, options.MerchantID, options.Limit, options.Offset)
	if err != nil {
		return core.ReconciliationFindingPage{}, err
	}
	defer rows.Close()
	page := core.ReconciliationFindingPage{Data: []core.ReconciliationFinding{}}
	for rows.Next() {
		var item core.ReconciliationFinding
		var expected, observed []byte
		if err := rows.Scan(&item.ID, &item.Domain, &item.OperationID, &item.AccountID, &item.MerchantID, &item.RuleCode, &item.Severity, &item.Status, &item.Currency, &item.Difference, &expected, &observed, &item.OccurrenceCount, &item.FirstSeenAt, &item.LastSeenAt, &item.ResolvedAt, &page.Total); err != nil {
			return core.ReconciliationFindingPage{}, err
		}
		_ = json.Unmarshal(expected, &item.Expected)
		_ = json.Unmarshal(observed, &item.Observed)
		page.Data = append(page.Data, item)
	}
	if err := rows.Err(); err != nil {
		return core.ReconciliationFindingPage{}, err
	}
	page.HasMore = options.Offset+len(page.Data) < page.Total
	return page, nil
}
