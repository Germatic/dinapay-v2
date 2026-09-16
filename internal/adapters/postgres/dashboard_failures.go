package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/jackc/pgx/v5"
)

func (s *Store) GetDashboardPaymentFailure(ctx context.Context, id string) (core.OperationalFailure, error) {
	return scanOperationalFailure(s.db.QueryRow(ctx, `SELECT 'payment',transaction_id::text,account_id,merchant_id,status,COALESCE(route_decision->>'provider',''),failure,provider_failure,updated_at FROM dinapay_v2_payments WHERE transaction_id=$1::uuid`, id))
}

func (s *Store) GetDashboardPayoutFailure(ctx context.Context, id string) (core.OperationalFailure, error) {
	return scanOperationalFailure(s.db.QueryRow(ctx, `SELECT 'payout',payout_id::text,account_id,merchant_id,status,COALESCE(route_decision->>'provider',''),failure,provider_failure,updated_at FROM dinapay_v2_payouts WHERE payout_id=$1::uuid`, id))
}

func scanOperationalFailure(row pgx.Row) (core.OperationalFailure, error) {
	var result core.OperationalFailure
	var failure, providerFailure []byte
	err := row.Scan(&result.ResourceType, &result.ResourceID, &result.AccountID, &result.MerchantID, &result.Status, &result.Provider, &failure, &providerFailure, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, core.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if len(failure) > 0 {
		_ = json.Unmarshal(failure, &result.Failure)
	}
	if len(providerFailure) > 0 {
		_ = json.Unmarshal(providerFailure, &result.ProviderFailure)
	}
	return result, nil
}
