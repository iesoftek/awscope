-- +goose Up
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS idx_edges_to_kind
  ON edges(to_key, kind);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_edges_to_kind;

-- +goose StatementEnd

