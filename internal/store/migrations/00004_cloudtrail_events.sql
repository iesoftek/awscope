-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS cloudtrail_events (
  event_id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  partition TEXT NOT NULL,
  region TEXT NOT NULL,
  event_time TEXT NOT NULL,
  event_source TEXT NOT NULL,
  event_name TEXT NOT NULL,
  action TEXT NOT NULL,
  service TEXT NOT NULL,
  resource_key TEXT,
  resource_type TEXT,
  resource_name TEXT,
  resource_arn TEXT,
  username TEXT,
  principal_arn TEXT,
  source_ip TEXT,
  user_agent TEXT,
  read_only TEXT,
  error_code TEXT,
  error_message TEXT,
  event_json TEXT NOT NULL DEFAULT '{}',
  indexed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_scope_time
  ON cloudtrail_events(account_id, region, event_time DESC);
CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_service_action_time
  ON cloudtrail_events(account_id, service, action, event_time DESC);
CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_resource_time
  ON cloudtrail_events(account_id, resource_key, event_time DESC);
CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_event_name
  ON cloudtrail_events(account_id, event_name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS cloudtrail_events;

-- +goose StatementEnd
