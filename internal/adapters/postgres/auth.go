package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Germatic/dinapay-v2/internal/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Auth struct {
	db          *pgxpool.Pool
	environment string
}

func NewAuth(db *pgxpool.Pool, environments ...string) *Auth {
	environment := "sandbox"
	if len(environments) > 0 && environments[0] != "" {
		environment = environments[0]
	}
	return &Auth{db: db, environment: environment}
}

func (a *Auth) Authenticate(ctx context.Context, raw string) (core.Principal, error) {
	if raw == "" {
		return core.Principal{}, core.ErrUnauthorized
	}
	h := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(h[:])
	var p core.Principal
	var merchant *string
	var scopes []byte
	err := a.db.QueryRow(ctx, `SELECT a.id,k.merchant_id,COALESCE(k.scopes,'[]'::jsonb) FROM api_keys k JOIN accounts a ON a.id=k.account_id WHERE k.key_hash=$1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND COALESCE(k.environment,'legacy') IN ('legacy',$2) AND COALESCE(k.allowed_api_versions,'["v1","v2"]'::jsonb) ? 'v2' AND a.status='active'`, hash, a.environment).Scan(&p.AccountID, &merchant, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, core.ErrUnauthorized
	}
	if err != nil {
		return p, err
	}
	if merchant != nil {
		p.MerchantID = *merchant
	}
	if err = json.Unmarshal(scopes, &p.Scopes); err != nil {
		return core.Principal{}, err
	}
	return p, nil
}

func (a *Auth) ResolveMerchant(ctx context.Context, p core.Principal, requested string) (string, error) {
	if p.MerchantID != "" {
		if requested != "" && requested != p.MerchantID {
			return "", core.ErrUnauthorized
		}
		return p.MerchantID, nil
	}
	if requested != "" {
		var ok bool
		err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM merchants WHERE id=$1 AND account_id=$2 AND status='active')`, requested, p.AccountID).Scan(&ok)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", core.ErrUnauthorized
		}
		return requested, nil
	}
	rows, err := a.db.Query(ctx, `SELECT id FROM merchants WHERE account_id=$1 AND status='active' ORDER BY created_at LIMIT 2`, p.AccountID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", core.ErrUnauthorized
	}
	return ids[0], nil
}
