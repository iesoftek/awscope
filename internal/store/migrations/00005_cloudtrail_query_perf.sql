-- +goose Up
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_scope_time_id
  ON cloudtrail_events(account_id, region, event_time DESC, event_id DESC);

CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_scope_service_action_time_id
  ON cloudtrail_events(account_id, region, service, action, event_time DESC, event_id DESC);

CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_scope_event_name_time_id
  ON cloudtrail_events(account_id, region, event_name, event_time DESC, event_id DESC);

CREATE INDEX IF NOT EXISTS idx_cloudtrail_events_scope_error_time
  ON cloudtrail_events(account_id, region, error_code, event_time DESC, event_id DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_cloudtrail_events_scope_error_time;
DROP INDEX IF EXISTS idx_cloudtrail_events_scope_event_name_time_id;
DROP INDEX IF EXISTS idx_cloudtrail_events_scope_service_action_time_id;
DROP INDEX IF EXISTS idx_cloudtrail_events_scope_time_id;

-- +goose StatementEnd
