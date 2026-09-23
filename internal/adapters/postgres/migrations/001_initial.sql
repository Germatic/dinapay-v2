CREATE TABLE IF NOT EXISTS dinapay_v2_idempotency (
  merchant_id    TEXT        NOT NULL,
  idempotency_key TEXT       NOT NULL,
  request_hash   TEXT        NOT NULL,
  transaction_id UUID        NOT NULL,
  status         TEXT        NOT NULL CHECK (status IN ('pending','complete')),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (merchant_id, idempotency_key)
);

ALTER TABLE dinapay_v2_idempotency ADD COLUMN IF NOT EXISTS external_id TEXT;

CREATE TABLE IF NOT EXISTS dinapay_v2_payments (
  transaction_id       UUID        PRIMARY KEY,
  account_id           TEXT        NOT NULL,
  merchant_id          TEXT        NOT NULL,
  external_id          TEXT        NOT NULL,
  status               TEXT        NOT NULL,
  amount               TEXT        NOT NULL,
  currency             TEXT        NOT NULL,
  payment_method       TEXT        NOT NULL,
  description          TEXT,
  creation_date        TIMESTAMPTZ NOT NULL,
  expiration_date      TIMESTAMPTZ NOT NULL,
  action_url           TEXT        NOT NULL,
  customer             JSONB,
  metadata             JSONB,
  payment_data         JSONB       NOT NULL,
  provider_payment_id  TEXT        NOT NULL,
  provider_reference   TEXT,
  route_decision       JSONB       NOT NULL,
  resource_version     BIGINT      NOT NULL DEFAULT 1,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS dinapay_v2_payments_account_idx
  ON dinapay_v2_payments (account_id, creation_date DESC);
CREATE INDEX IF NOT EXISTS dinapay_v2_payments_merchant_idx
  ON dinapay_v2_payments (merchant_id, creation_date DESC);

-- Durable delivery of confirmed V2 payins to Dinacore. The payment state and
-- its balance credit are committed atomically; this worker-owned outbox then
-- retries the external ledger call without losing or duplicating credits.
CREATE TABLE IF NOT EXISTS dinacore_balance_outbox (
  id              UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  ref_type        TEXT          NOT NULL,
  ref_id          TEXT          NOT NULL,
  account_id      TEXT          NOT NULL,
  amount          NUMERIC(36,18) NOT NULL,
  currency        TEXT          NOT NULL,
  sent            BOOLEAN       NOT NULL DEFAULT false,
  attempt_count   INTEGER       NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ   NOT NULL DEFAULT now(),
  last_error      TEXT,
  created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
  sent_at         TIMESTAMPTZ,
  UNIQUE (ref_type, ref_id)
);
CREATE INDEX IF NOT EXISTS dinacore_balance_outbox_pending_idx
  ON dinacore_balance_outbox (next_attempt_at, created_at)
  WHERE sent = false;

ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS provider_payment_id TEXT;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS provider_reference TEXT;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS confirmation_date TIMESTAMPTZ;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS received_amount TEXT;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS pricing JSONB;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS failure JSONB;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS provider_failure JSONB;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS success_url TEXT;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS cancel_url TEXT;
ALTER TABLE dinapay_v2_payments ADD COLUMN IF NOT EXISTS payer JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Preserve all historical payments, including any pre-existing duplicate
-- externalId values, while reserving one representative of every historical
-- merchant-scoped identifier. New creates always populate external_id and the
-- unique index prevents duplicate business orders even with concurrent keys.
WITH ranked_external_ids AS (
  SELECT i.merchant_id, i.idempotency_key, p.external_id,
         row_number() OVER (
           PARTITION BY i.merchant_id, p.external_id
           ORDER BY p.creation_date, p.transaction_id
         ) AS position
  FROM dinapay_v2_idempotency i
  JOIN dinapay_v2_payments p ON p.transaction_id=i.transaction_id
  WHERE p.external_id<>''
)
UPDATE dinapay_v2_idempotency i
SET external_id=ranked.external_id
FROM ranked_external_ids ranked
WHERE i.merchant_id=ranked.merchant_id
  AND i.idempotency_key=ranked.idempotency_key
  AND ranked.position=1
  AND i.external_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS dinapay_v2_idempotency_merchant_external_uidx
  ON dinapay_v2_idempotency (merchant_id,external_id)
  WHERE external_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS dinapay_v2_provider_events (
  event_id       TEXT        PRIMARY KEY,
  event_type     TEXT        NOT NULL,
  transaction_id UUID       NOT NULL,
  provider       TEXT        NOT NULL,
  source         TEXT        NOT NULL,
  payload        JSONB       NOT NULL,
  received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  changed_state  BOOLEAN     NOT NULL DEFAULT false
);
ALTER TABLE dinapay_v2_provider_events ALTER COLUMN transaction_id DROP NOT NULL;
ALTER TABLE dinapay_v2_provider_events ADD COLUMN IF NOT EXISTS payout_id UUID;

-- Recover the confirmation instant for orders confirmed before the column was
-- introduced. received_at is when Dinaria durably accepted the provider event;
-- do not use updated_at because later refunds would rewrite that timestamp.
UPDATE dinapay_v2_payments p
SET confirmation_date = confirmed.first_received_at
FROM (
  SELECT transaction_id, MIN(received_at) AS first_received_at
  FROM dinapay_v2_provider_events
  WHERE transaction_id IS NOT NULL AND payload->'data'->>'status' = 'confirmed'
  GROUP BY transaction_id
) confirmed
WHERE p.transaction_id = confirmed.transaction_id
  AND p.confirmation_date IS NULL;

CREATE INDEX IF NOT EXISTS dinapay_v2_payments_account_confirmation_idx
  ON dinapay_v2_payments (account_id, confirmation_date DESC, transaction_id DESC)
  WHERE confirmation_date IS NOT NULL;
CREATE INDEX IF NOT EXISTS dinapay_v2_payments_merchant_confirmation_idx
  ON dinapay_v2_payments (merchant_id, confirmation_date DESC, transaction_id DESC)
  WHERE confirmation_date IS NOT NULL;

-- Historical pricing uses the schedule effective at confirmation, never the
-- current schedule. V2 has no sub-account payments yet, so platform fee is 0.
UPDATE dinapay_v2_payments p
SET received_amount = COALESCE(p.received_amount,p.amount),
    pricing = COALESCE(p.pricing,jsonb_build_object(
      'feeAmount',COALESCE((SELECT GREATEST(ROUND(p.amount::numeric*f.payin_fee_pct,8),f.payin_fee_min)::text
                            FROM account_fees f WHERE f.account_id=p.account_id AND f.currency=p.currency
                              AND f.effective_from<=p.confirmation_date ORDER BY f.effective_from DESC LIMIT 1),'0'),
      'platformFeeAmount','0'))
WHERE p.confirmation_date IS NOT NULL AND (p.received_amount IS NULL OR p.pricing IS NULL);

-- Additive compatibility marker. Existing registrations remain V1.
ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS api_version TEXT NOT NULL DEFAULT '1';
ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS event_types TEXT[];
CREATE INDEX IF NOT EXISTS webhooks_api_version_idx ON webhooks (api_version);

CREATE TABLE IF NOT EXISTS dinapay_v2_refunds (
  refund_id UUID PRIMARY KEY,
  transaction_id UUID NOT NULL REFERENCES dinapay_v2_payments(transaction_id),
  account_id TEXT NOT NULL,
  merchant_id TEXT NOT NULL,
  external_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  status TEXT NOT NULL,
  amount TEXT NOT NULL,
  currency TEXT NOT NULL,
  reason TEXT,
  metadata JSONB,
  provider_refund_id TEXT,
  provider_status TEXT,
  balance_debited BOOLEAN NOT NULL DEFAULT false,
  provider_submitted BOOLEAN NOT NULL DEFAULT false,
  resource_version BIGINT NOT NULL DEFAULT 1,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT,
  creation_date TIMESTAMPTZ NOT NULL DEFAULT now(),
  completion_date TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (merchant_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS dinapay_v2_refunds_pending_idx ON dinapay_v2_refunds(next_attempt_at,creation_date)
  WHERE status IN ('pending_debit','pending_provider','pending','pending_compensation');
ALTER TABLE dinapay_v2_refunds ADD COLUMN IF NOT EXISTS failure JSONB;
ALTER TABLE dinapay_v2_refunds ADD COLUMN IF NOT EXISTS provider_failure JSONB;

CREATE TABLE IF NOT EXISTS dinapay_v2_payouts (
  payout_id UUID PRIMARY KEY, account_id TEXT NOT NULL, merchant_id TEXT NOT NULL,
  external_id TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL,
  status TEXT NOT NULL, source_amount TEXT NOT NULL, source_currency TEXT NOT NULL,
  destination JSONB NOT NULL, pricing JSONB, remitter JSONB, description TEXT, metadata JSONB,
  provider_payout_id TEXT, provider_reference TEXT, provider_status TEXT, route_decision JSONB NOT NULL,
  balance_debited BOOLEAN NOT NULL DEFAULT false, provider_submitted BOOLEAN NOT NULL DEFAULT false,
  resource_version BIGINT NOT NULL DEFAULT 1, attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_error TEXT,
  creation_date TIMESTAMPTZ NOT NULL DEFAULT now(), confirmation_date TIMESTAMPTZ,
  failure_date TIMESTAMPTZ, cancellation_date TIMESTAMPTZ, reversal_date TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (merchant_id,idempotency_key)
);
ALTER TABLE dinapay_v2_payouts ADD COLUMN IF NOT EXISTS failure JSONB;
ALTER TABLE dinapay_v2_payouts ADD COLUMN IF NOT EXISTS provider_failure JSONB;
CREATE TABLE IF NOT EXISTS dinapay_v2_payout_idempotency (
  merchant_id TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL,
  payout_id UUID NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','complete')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(merchant_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS dinapay_v2_payouts_pending_idx ON dinapay_v2_payouts(next_attempt_at,creation_date)
  WHERE status IN ('pending_debit','pending_provider','provider_unknown','processing','pending_compensation','pending_compensation_cancelled','pending_compensation_reversed');
