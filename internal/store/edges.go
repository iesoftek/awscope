package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"awscope/internal/graph"
)

func (s *Store) UpsertEdges(ctx context.Context, edges []graph.RelationshipEdge) error {
	if len(edges) == 0 {
		return nil
	}

	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO edges(from_key, to_key, kind, meta_json, collected_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(from_key, to_key, kind) DO UPDATE SET
  meta_json=excluded.meta_json,
  collected_at=excluded.collected_at
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range edges {
		metaJSON, err := json.Marshal(orEmptyAnyMap(e.Meta))
		if err != nil {
			return err
		}
		collectedAt := e.CollectedAt
		if collectedAt.IsZero() {
			collectedAt = now
		}
		if _, err := stmt.ExecContext(ctx, string(e.From), string(e.To), e.Kind, string(metaJSON), collectedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) EdgesFrom(ctx context.Context, from graph.ResourceKey) ([]graph.RelationshipEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT from_key, to_key, kind, meta_json, collected_at
FROM edges
WHERE from_key = ?
ORDER BY kind, to_key
`, string(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []graph.RelationshipEdge
	for rows.Next() {
		var (
			fromKey    string
			toKey      string
			kind       string
			metaJSON   string
			collected  string
			collectedT time.Time
		)
		if err := rows.Scan(&fromKey, &toKey, &kind, &metaJSON, &collected); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metaJSON), &map[string]any{})
		meta := map[string]any{}
		_ = json.Unmarshal([]byte(metaJSON), &meta)
		collectedT, _ = time.Parse(time.RFC3339Nano, collected)
		out = append(out, graph.RelationshipEdge{
			From:        graph.ResourceKey(fromKey),
			To:          graph.ResourceKey(toKey),
			Kind:        kind,
			Meta:        meta,
			CollectedAt: collectedT,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
