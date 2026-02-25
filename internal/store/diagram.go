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

func (s *Store) ListResourcesByAccountAndRegion(ctx context.Context, accountID, region string) ([]ResourceSummary, error) {
	accountID = strings.TrimSpace(accountID)
	region = strings.TrimSpace(region)
	if accountID == "" || region == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
  r.resource_key, r.account_id, r.partition, r.region, r.service, r.type,
  r.arn, r.primary_id, r.display_name,
  r.tags_json, r.attributes_json,
  r.collected_at, r.updated_at,
  c.est_monthly_usd, c.basis
FROM resources r
LEFT JOIN resource_costs c ON c.resource_key = r.resource_key
WHERE r.account_id = ? AND r.region = ?
ORDER BY r.service ASC, r.type ASC, r.display_name ASC
`, accountID, region)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResourceSummaries(rows)
}

func (s *Store) ListEdgesByResourceKeys(ctx context.Context, keys []graph.ResourceKey) ([]graph.RelationshipEdge, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	uniq := map[string]struct{}{}
	for _, k := range keys {
		if strings.TrimSpace(string(k)) == "" {
			continue
		}
		uniq[string(k)] = struct{}{}
	}
	if len(uniq) == 0 {
		return nil, nil
	}

	holders := make([]string, 0, len(uniq))
	args := make([]any, 0, len(uniq)*2)
	for k := range uniq {
		holders = append(holders, "?")
		args = append(args, k)
	}
	for k := range uniq {
		args = append(args, k)
	}

	q := fmt.Sprintf(`
SELECT from_key, to_key, kind, meta_json, collected_at
FROM edges
WHERE from_key IN (%s) AND to_key IN (%s)
ORDER BY kind ASC, from_key ASC, to_key ASC
`, strings.Join(holders, ","), strings.Join(holders, ","))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []graph.RelationshipEdge
	for rows.Next() {
		var (
			fromKey   string
			toKey     string
			kind      string
			metaJSON  string
			collected string
		)
		if err := rows.Scan(&fromKey, &toKey, &kind, &metaJSON, &collected); err != nil {
			return nil, err
		}
		meta := map[string]any{}
		_ = json.Unmarshal([]byte(metaJSON), &meta)
		t, _ := time.Parse(time.RFC3339Nano, collected)
		out = append(out, graph.RelationshipEdge{
			From:        graph.ResourceKey(fromKey),
			To:          graph.ResourceKey(toKey),
			Kind:        kind,
			Meta:        meta,
			CollectedAt: t,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListLinkedGlobalResourcesForRegion returns global resources that have at least one direct edge
// to a resource in the selected region for the given account, and those cross-region edges.
func (s *Store) ListLinkedGlobalResourcesForRegion(ctx context.Context, accountID, region string) ([]ResourceSummary, []graph.RelationshipEdge, error) {
	accountID = strings.TrimSpace(accountID)
	region = strings.TrimSpace(region)
	if accountID == "" || region == "" {
		return nil, nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT e.from_key, e.to_key, e.kind, e.meta_json, e.collected_at, rf.region, rt.region
FROM edges e
JOIN resources rf ON rf.resource_key = e.from_key
JOIN resources rt ON rt.resource_key = e.to_key
WHERE rf.account_id = ? AND rt.account_id = ?
  AND (
    (rf.region = ? AND rt.region = 'global') OR
    (rf.region = 'global' AND rt.region = ?)
  )
ORDER BY e.kind ASC, e.from_key ASC, e.to_key ASC
`, accountID, accountID, region, region)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	globalKeys := map[string]struct{}{}
	var crossEdges []graph.RelationshipEdge

	for rows.Next() {
		var (
			fromKey    string
			toKey      string
			kind       string
			metaJSON   string
			collected  string
			fromRegion string
			toRegion   string
		)
		if err := rows.Scan(&fromKey, &toKey, &kind, &metaJSON, &collected, &fromRegion, &toRegion); err != nil {
			return nil, nil, err
		}
		meta := map[string]any{}
		_ = json.Unmarshal([]byte(metaJSON), &meta)
		t, _ := time.Parse(time.RFC3339Nano, collected)
		crossEdges = append(crossEdges, graph.RelationshipEdge{
			From:        graph.ResourceKey(fromKey),
			To:          graph.ResourceKey(toKey),
			Kind:        kind,
			Meta:        meta,
			CollectedAt: t,
		})
		if fromRegion == "global" {
			globalKeys[fromKey] = struct{}{}
		}
		if toRegion == "global" {
			globalKeys[toKey] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(globalKeys) == 0 {
		return nil, crossEdges, nil
	}

	holders := make([]string, 0, len(globalKeys))
	args := make([]any, 0, len(globalKeys))
	for k := range globalKeys {
		holders = append(holders, "?")
		args = append(args, k)
	}

	q := fmt.Sprintf(`
SELECT
  r.resource_key, r.account_id, r.partition, r.region, r.service, r.type,
  r.arn, r.primary_id, r.display_name,
  r.tags_json, r.attributes_json,
  r.collected_at, r.updated_at,
  c.est_monthly_usd, c.basis
FROM resources r
LEFT JOIN resource_costs c ON c.resource_key = r.resource_key
WHERE r.resource_key IN (%s)
ORDER BY r.service ASC, r.type ASC, r.display_name ASC
`, strings.Join(holders, ","))
	resRows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer resRows.Close()

	globalResources, err := scanResourceSummaries(resRows)
	if err != nil {
		return nil, nil, err
	}
	return globalResources, crossEdges, nil
}

func scanResourceSummaries(rows *sql.Rows) ([]ResourceSummary, error) {
	var out []ResourceSummary
	for rows.Next() {
		var (
			keyStr      string
			accountID   string
			partition   string
			region      string
			service     string
			typ         string
			arn         sql.NullString
			primaryID   string
			displayName string
			tagsJSON    string
			attrsJSON   string
			collectedAt string
			updatedAt   string
			estUSD      sql.NullFloat64
			costBasis   sql.NullString
		)
		if err := rows.Scan(
			&keyStr, &accountID, &partition, &region, &service, &typ,
			&arn, &primaryID, &displayName,
			&tagsJSON, &attrsJSON,
			&collectedAt, &updatedAt,
			&estUSD, &costBasis,
		); err != nil {
			return nil, err
		}

		var tags map[string]string
		_ = json.Unmarshal([]byte(tagsJSON), &tags)
		var attrs map[string]any
		_ = json.Unmarshal([]byte(attrsJSON), &attrs)

		colT, _ := time.Parse(time.RFC3339Nano, collectedAt)
		upT, _ := time.Parse(time.RFC3339Nano, updatedAt)

		var estPtr *float64
		if estUSD.Valid {
			v := estUSD.Float64
			estPtr = &v
		}

		out = append(out, ResourceSummary{
			Key:           graph.ResourceKey(keyStr),
			DisplayName:   displayName,
			AccountID:     accountID,
			Partition:     partition,
			Region:        region,
			Service:       service,
			Type:          typ,
			Arn:           arn.String,
			PrimaryID:     primaryID,
			Tags:          tags,
			Attributes:    attrs,
			CollectedAt:   colT,
			UpdatedAt:     upT,
			EstMonthlyUSD: estPtr,
			CostBasis:     costBasis.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
