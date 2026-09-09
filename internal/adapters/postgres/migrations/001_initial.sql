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
  route_decision       JSONB       NOT NULL,
  resource_version     BIGINT      NOT NULL DEFAULT 1,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS dinapay_v2_payments_account_idx
  ON dinapay_v2_payments (account_id, creation_date DESC);
CREATE INDEX IF NOT EXISTS dinapay_v2_payments_merchant_idx
  ON dinapay_v2_payments (merchant_id, creation_date DESC);

-- Additive compatibility marker. Existing registrations remain V1.
ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS api_version TEXT NOT NULL DEFAULT '1';
CREATE INDEX IF NOT EXISTS webhooks_api_version_idx ON webhooks (api_version);
