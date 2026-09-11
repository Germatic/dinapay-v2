package postgres

import (
	"context"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

func (s *Store) DashboardSummary(ctx context.Context, accountID string, o core.DashboardSummaryOptions) (core.DashboardSummary, error) {
	rows, err := s.db.Query(ctx, dashboardSummarySQL, accountID, o.MerchantID, o.Direction, o.Status, o.Currency, o.ExternalID, o.CreatedAfter, o.CreatedBefore, o.ConfirmedAfter, o.ConfirmedBefore)
	if err != nil {
		return core.DashboardSummary{}, err
	}
	defer rows.Close()
	out := core.DashboardSummary{Currencies: []core.DashboardCurrencySummary{}}
	if o.CreatedAfter != nil {
		out.Period.From = o.CreatedAfter.UTC().Format(time.RFC3339)
	}
	if o.CreatedBefore != nil {
		out.Period.To = o.CreatedBefore.UTC().Format(time.RFC3339)
	}
	for rows.Next() {
		var item core.DashboardCurrencySummary
		if err = rows.Scan(&item.Currency, &item.PayinCount, &item.PayinVolume, &item.PayinSettledCount, &item.PayinSettledVolume, &item.PayoutCount, &item.PayoutVolume, &item.PayoutSettledCount, &item.PayoutSettledVolume, &item.PlatformFeeEarned, &item.DinariaFeeCharged, &item.FeesComplete); err != nil {
			return out, err
		}
		out.Currencies = append(out.Currencies, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM sub_accounts WHERE ($1='' OR account_id=$1)`, accountID).Scan(&out.SubAccountCount); err != nil {
		return out, err
	}
	if accountID == "" {
		if err = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM accounts WHERE status='active'`).Scan(&out.AccountCount); err != nil {
			return out, err
		}
	}
	return out, nil
}

const dashboardSummarySQL = `WITH movements AS (
 SELECT account_id,merchant_id,external_id,'in'::text direction,status,currency,COALESCE(NULLIF(received_amount,''),amount)::numeric amount,creation_date,confirmation_date,
        CASE WHEN COALESCE(pricing->>'feeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$' THEN (pricing->>'feeAmount')::numeric END,
        CASE WHEN COALESCE(pricing->>'platformFeeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$' THEN (pricing->>'platformFeeAmount')::numeric END,
        COALESCE(pricing->>'feeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$' AND COALESCE(pricing->>'platformFeeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$'
 FROM dinapay_v2_payments
 UNION ALL
 SELECT COALESCE(NULLIF(p.account_id,''),m.account_id,''),p.merchant_id,COALESCE(p.external_id,''),'in',p.status,p.currency,
		CASE WHEN COALESCE(p.received_amount,'') ~ '^-?[0-9]+(\.[0-9]+)?$' THEN p.received_amount::numeric ELSE p.amount::numeric END,
        p.created_at,p.received_at,COALESCE(p.fee_amount,0),COALESCE(p.platform_fee_amount,0),true
 FROM payments p LEFT JOIN merchants m ON m.id=p.merchant_id
 UNION ALL
 SELECT account_id,merchant_id,external_id,'out',status,source_currency,source_amount::numeric,creation_date,confirmation_date,
        CASE WHEN COALESCE(pricing->>'feeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$' THEN (pricing->>'feeAmount')::numeric END,
        CASE WHEN COALESCE(pricing->>'platformFeeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$' THEN (pricing->>'platformFeeAmount')::numeric END,
        COALESCE(pricing->>'feeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$' AND COALESCE(pricing->>'platformFeeAmount','') ~ '^-?[0-9]+(\.[0-9]+)?$'
 FROM dinapay_v2_payouts
 UNION ALL
 SELECT COALESCE(p.account_id,''),COALESCE(p.merchant_id,''),COALESCE(p.external_id,''),'out',CASE WHEN p.status='completed' THEN 'confirmed' ELSE p.status END,p.currency,
        p.amount,p.created_at,p.completed_at,COALESCE(p.fee_amount,0),COALESCE(p.platform_fee_amount,0),true
 FROM payouts p
), filtered AS (
 SELECT *,status IN ('confirmed','matched') settled FROM movements
 WHERE ($1='' OR account_id=$1) AND ($2='' OR merchant_id=$2) AND ($3='' OR direction=$3)
   AND ($4='' OR status=$4) AND ($5='' OR currency=$5) AND ($6='' OR external_id=$6)
   AND ($7::timestamptz IS NULL OR creation_date >= $7) AND ($8::timestamptz IS NULL OR creation_date < $8)
   AND ($9::timestamptz IS NULL OR confirmation_date >= $9) AND ($10::timestamptz IS NULL OR confirmation_date < $10)
) SELECT currency,
 COUNT(*) FILTER (WHERE direction='in'),COALESCE(SUM(amount) FILTER (WHERE direction='in'),0)::text,
 COUNT(*) FILTER (WHERE direction='in' AND settled),COALESCE(SUM(amount) FILTER (WHERE direction='in' AND settled),0)::text,
 COUNT(*) FILTER (WHERE direction='out'),COALESCE(SUM(amount) FILTER (WHERE direction='out'),0)::text,
 COUNT(*) FILTER (WHERE direction='out' AND settled),COALESCE(SUM(amount) FILTER (WHERE direction='out' AND settled),0)::text,
 CASE WHEN BOOL_AND(NOT settled OR fees_known) THEN COALESCE(SUM(platform_fee) FILTER (WHERE settled),0)::text END,
 CASE WHEN BOOL_AND(NOT settled OR fees_known) THEN COALESCE(SUM(dinaria_fee) FILTER (WHERE settled),0)::text END,
 BOOL_AND(NOT settled OR fees_known)
 FROM filtered GROUP BY currency ORDER BY currency`
