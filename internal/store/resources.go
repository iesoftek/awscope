package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
)

type ResourceSummary struct {
	Key         graph.ResourceKey
	DisplayName string
	AccountID   string
	Partition   string
	Region      string
	Service     string
	Type        string
	Arn         string
	PrimaryID   string
	Tags        map[string]string
	Attributes  map[string]any
	CollectedAt time.Time
	UpdatedAt   time.Time

	// Optional cost estimate (may be nil when unknown/unpriced).
	EstMonthlyUSD *float64
	CostBasis     string
}

func (s *Store) UpsertResources(ctx context.Context, nodes []graph.ResourceNode) error {
	return s.UpsertResourcesWithScan(ctx, nodes, "")
}

func (s *Store) UpsertResourcesWithScan(ctx context.Context, nodes []graph.ResourceNode, scanID string) error {
	if len(nodes) == 0 {
		return nil
	}

	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Full upsert for "real" nodes.
	stmtUpsert, err := tx.PrepareContext(ctx, `
INSERT INTO resources(
  resource_key, account_id, partition, region, service, type,
  arn, primary_id, display_name,
  tags_json, attributes_json, raw_json,
  collected_at, updated_at,
  lifecycle_state, last_seen_scan_id, missing_since
) VALUES (
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?,
  ?, ?,
  ?, ?, ?
)
ON CONFLICT(resource_key) DO UPDATE SET
  account_id=excluded.account_id,
  partition=excluded.partition,
  region=excluded.region,
  service=excluded.service,
  type=excluded.type,
  arn=excluded.arn,
  primary_id=excluded.primary_id,
  display_name=excluded.display_name,
  tags_json=excluded.tags_json,
  attributes_json=excluded.attributes_json,
  raw_json=excluded.raw_json,
  collected_at=excluded.collected_at,
  updated_at=excluded.updated_at,
  lifecycle_state='active',
  last_seen_scan_id=CASE
    WHEN excluded.last_seen_scan_id IS NULL OR excluded.last_seen_scan_id = '' THEN resources.last_seen_scan_id
    ELSE excluded.last_seen_scan_id
  END,
  missing_since=NULL
`)
	if err != nil {
		return err
	}
	defer stmtUpsert.Close()

	// Stub nodes are placeholders created to satisfy edges. They should never clobber a richer node
	// from another provider/scan step. We still insert them if the resource doesn't exist yet.
	stmtStub, err := tx.PrepareContext(ctx, `
INSERT INTO resources(
  resource_key, account_id, partition, region, service, type,
  arn, primary_id, display_name,
  tags_json, attributes_json, raw_json,
  collected_at, updated_at,
  lifecycle_state, last_seen_scan_id, missing_since
) VALUES (
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?,
  ?, ?,
  ?, ?, ?
)
ON CONFLICT(resource_key) DO UPDATE SET
  lifecycle_state='active',
  last_seen_scan_id=CASE
    WHEN excluded.last_seen_scan_id IS NULL OR excluded.last_seen_scan_id = '' THEN resources.last_seen_scan_id
    ELSE excluded.last_seen_scan_id
  END,
  missing_since=NULL,
  updated_at=excluded.updated_at
`)
	if err != nil {
		return err
	}
	defer stmtStub.Close()

	for _, n := range nodes {
		partition, accountID, region, resourceType, primaryID, err := graph.ParseResourceKey(n.Key)
		if err != nil {
			return fmt.Errorf("parse key: %w", err)
		}

		isStub := len(n.Tags) == 0 &&
			len(n.Attributes) == 0 &&
			(strings.TrimSpace(n.Arn) == "") &&
			(len(n.Raw) == 0 || string(n.Raw) == "{}")

		tagsJSON, err := json.Marshal(orEmptyStringMap(n.Tags))
		if err != nil {
			return err
		}
		attrsJSON, err := json.Marshal(orEmptyAnyMap(n.Attributes))
		if err != nil {
			return err
		}
		raw := []byte("{}")
		if len(n.Raw) > 0 {
			raw = n.Raw
		}

		collectedAt := n.CollectedAt
		if collectedAt.IsZero() {
			collectedAt = now
		}

		stmt := stmtUpsert
		if isStub {
			stmt = stmtStub
		}
		if _, err := stmt.ExecContext(
			ctx,
			string(n.Key), accountID, partition, region, n.Service, resourceType,
			nullIfEmpty(n.Arn), primaryID, n.DisplayName,
			string(tagsJSON), string(attrsJSON), string(raw),
			collectedAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
			"active", nullIfEmpty(strings.TrimSpace(scanID)), nil,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) GetResource(ctx context.Context, key graph.ResourceKey) (graph.ResourceNode, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT
  resource_key, region, service, type, arn, primary_id, display_name,
  tags_json, attributes_json, raw_json, collected_at
FROM resources
WHERE resource_key = ?
`, string(key))

	var (
		keyStr      string
		region      string
		service     string
		typ         string
		arn         sql.NullString
		primaryID   string
		displayName string
		tagsJSON    string
		attrsJSON   string
		rawJSON     string
		collectedAt string
	)
	if err := row.Scan(&keyStr, &region, &service, &typ, &arn, &primaryID, &displayName, &tagsJSON, &attrsJSON, &rawJSON, &collectedAt); err != nil {
		return graph.ResourceNode{}, err
	}

	var tags map[string]string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		return graph.ResourceNode{}, err
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(attrsJSON), &attrs); err != nil {
		return graph.ResourceNode{}, err
	}

	t, err := time.Parse(time.RFC3339Nano, collectedAt)
	if err != nil {
		// Be permissive; keep zero time if unexpected.
		t = time.Time{}
	}

	return graph.ResourceNode{
		Key:         graph.ResourceKey(keyStr),
		DisplayName: displayName,
		Service:     service,
		Type:        typ,
		Arn:         arn.String,
		PrimaryID:   primaryID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         json.RawMessage(rawJSON),
		CollectedAt: t,
		Source:      "",
	}, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func orEmptyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func orEmptyAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
