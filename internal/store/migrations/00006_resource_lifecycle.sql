-- +goose Up
-- +goose StatementBegin

ALTER TABLE resources ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'active';
ALTER TABLE resources ADD COLUMN last_seen_scan_id TEXT;
ALTER TABLE resources ADD COLUMN missing_since TEXT;

CREATE INDEX IF NOT EXISTS idx_resources_scope_state
  ON resources(account_id, service, region, lifecycle_state);
CREATE INDEX IF NOT EXISTS idx_resources_last_seen_scan
  ON resources(last_seen_scan_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_resources_last_seen_scan;
DROP INDEX IF EXISTS idx_resources_scope_state;

-- SQLite does not support DROP COLUMN reliably in-place for this project setup.

-- +goose StatementEnd
