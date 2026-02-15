-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS pricing_cache (
  cache_key TEXT PRIMARY KEY,
  partition TEXT NOT NULL,
  service_code TEXT NOT NULL,
  price_kind TEXT NOT NULL,
  aws_region TEXT NOT NULL,
  location TEXT NOT NULL,
  filters_json TEXT NOT NULL,
  unit TEXT NOT NULL,
  usd REAL,
  raw_json TEXT NOT NULL DEFAULT '{}',
  retrieved_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pricing_cache_service_kind
  ON pricing_cache(service_code, price_kind);
CREATE INDEX IF NOT EXISTS idx_pricing_cache_region_kind
  ON pricing_cache(aws_region, price_kind);

CREATE TABLE IF NOT EXISTS resource_costs (
  resource_key TEXT PRIMARY KEY,

  account_id TEXT NOT NULL,
  partition TEXT NOT NULL,
  region TEXT NOT NULL,
  service TEXT NOT NULL,
  type TEXT NOT NULL,

  est_monthly_usd REAL,
  currency TEXT NOT NULL DEFAULT 'USD',
  basis TEXT NOT NULL,
  breakdown_json TEXT NOT NULL DEFAULT '{}',

  computed_at TEXT NOT NULL,
  source TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_resource_costs_scope
  ON resource_costs(account_id, region, service, type);
CREATE INDEX IF NOT EXISTS idx_resource_costs_service
  ON resource_costs(account_id, service);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS resource_costs;
DROP TABLE IF EXISTS pricing_cache;
-- +goose StatementEnd

