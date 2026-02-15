-- +goose Up
-- +goose StatementBegin

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS accounts (
  account_id TEXT PRIMARY KEY,
  partition TEXT NOT NULL,
  last_seen_at TEXT
);

CREATE TABLE IF NOT EXISTS profiles (
  profile_name TEXT PRIMARY KEY,
  account_id TEXT,
  role_arn TEXT,
  last_used_at TEXT
);

CREATE TABLE IF NOT EXISTS regions (
  region TEXT PRIMARY KEY
);

-- resources: latest view
CREATE TABLE IF NOT EXISTS resources (
  resource_key TEXT PRIMARY KEY,

  account_id TEXT NOT NULL,
  partition TEXT NOT NULL,
  region TEXT NOT NULL,
  service TEXT NOT NULL,
  type TEXT NOT NULL,

  arn TEXT,
  primary_id TEXT NOT NULL,
  display_name TEXT NOT NULL,

  tags_json TEXT NOT NULL DEFAULT '{}',
  attributes_json TEXT NOT NULL DEFAULT '{}',
  raw_json TEXT NOT NULL DEFAULT '{}',

  collected_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_resources_scope
  ON resources(account_id, region, service, type);
CREATE INDEX IF NOT EXISTS idx_resources_arn
  ON resources(arn);
CREATE INDEX IF NOT EXISTS idx_resources_display_name
  ON resources(display_name);

CREATE TABLE IF NOT EXISTS edges (
  from_key TEXT NOT NULL,
  to_key TEXT NOT NULL,
  kind TEXT NOT NULL,
  meta_json TEXT NOT NULL DEFAULT '{}',
  collected_at TEXT NOT NULL,
  PRIMARY KEY(from_key, to_key, kind)
);

CREATE INDEX IF NOT EXISTS idx_edges_from_kind
  ON edges(from_key, kind);

CREATE TABLE IF NOT EXISTS scan_runs (
  scan_id TEXT PRIMARY KEY,
  started_at TEXT NOT NULL,
  ended_at TEXT,
  profile_name TEXT NOT NULL,
  scope_json TEXT NOT NULL DEFAULT '{}',
  status TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS action_runs (
  action_run_id TEXT PRIMARY KEY,
  started_at TEXT NOT NULL,
  ended_at TEXT,

  profile_name TEXT NOT NULL,
  account_id TEXT,
  region TEXT,

  resource_key TEXT NOT NULL,
  action_id TEXT NOT NULL,
  input_json TEXT NOT NULL DEFAULT '{}',
  result_json TEXT NOT NULL DEFAULT '{}',
  status TEXT NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS action_runs;
DROP TABLE IF EXISTS scan_runs;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS resources;
DROP TABLE IF EXISTS regions;
DROP TABLE IF EXISTS profiles;
DROP TABLE IF EXISTS accounts;
-- +goose StatementEnd

