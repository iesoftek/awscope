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

func (s *Store) CountResources(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM resources WHERE lifecycle_state = 'active'`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) ListResourceSummaries(ctx context.Context, limit int) ([]ResourceSummary, error) {
	if limit <= 0 {
		limit = 100
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
WHERE r.lifecycle_state = 'active'
ORDER BY r.updated_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *Store) CountEdges(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM edges`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

type ServiceTypeCount struct {
	Service string
	Type    string
	Count   int
}

func (s *Store) CountResourcesByType(ctx context.Context) ([]ServiceTypeCount, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT service, type, COUNT(1)
FROM resources
WHERE lifecycle_state = 'active'
GROUP BY service, type
ORDER BY service, type
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ServiceTypeCount
	for rows.Next() {
		var r ServiceTypeCount
		if err := rows.Scan(&r.Service, &r.Type, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) CountResourcesByService(ctx context.Context, service string) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM resources WHERE service = ? AND lifecycle_state = 'active'`, service)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) ListResourceSummariesByService(ctx context.Context, service, filter string, limit int) ([]ResourceSummary, error) {
	return s.ListResourceSummariesByServiceAndRegions(ctx, service, nil, filter, limit)
}

func (s *Store) ListResourceSummariesByServiceAndRegions(ctx context.Context, service string, regions []string, filter string, limit int) ([]ResourceSummary, error) {
	if limit <= 0 {
		limit = 500
	}
	like := "%"
	if filter != "" {
		like = "%" + filter + "%"
	}

	regionClause := ""
	args := []any{service, like, like, like}
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		// Count query does not join; do not qualify column names.
		regionClause = fmt.Sprintf(" AND r.region IN (%s)", strings.Join(holders, ","))
	}
	args = append(args, limit)

	q := fmt.Sprintf(`
SELECT
  r.resource_key, r.account_id, r.partition, r.region, r.service, r.type,
  r.arn, r.primary_id, r.display_name,
  r.tags_json, r.attributes_json,
  r.collected_at, r.updated_at,
  c.est_monthly_usd, c.basis
FROM resources r
LEFT JOIN resource_costs c ON c.resource_key = r.resource_key
WHERE r.service = ?
  AND r.lifecycle_state = 'active'
  AND (
    r.display_name LIKE ?
    OR r.primary_id LIKE ?
    OR r.arn LIKE ?
  )%s
ORDER BY r.display_name ASC
LIMIT ?
`, regionClause)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *Store) CountResourceSummariesByServiceAndRegions(ctx context.Context, service string, regions []string, filter string) (int, error) {
	like := "%"
	if filter != "" {
		like = "%" + filter + "%"
	}

	regionClause := ""
	args := []any{service, like, like, like, like, like, like}
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		regionClause = fmt.Sprintf(" AND region IN (%s)", strings.Join(holders, ","))
	}

	q := fmt.Sprintf(`
SELECT COUNT(1)
FROM resources
WHERE service = ?
  AND lifecycle_state = 'active'
  AND (
    display_name LIKE ?
    OR primary_id LIKE ?
    OR arn LIKE ?
    OR type LIKE ?
    OR tags_json LIKE ?
    OR attributes_json LIKE ?
  )%s
`, regionClause)

	row := s.db.QueryRowContext(ctx, q, args...)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) ListResourceSummariesByServiceAndRegionsPaged(ctx context.Context, service string, regions []string, filter string, limit, offset int) ([]ResourceSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	like := "%"
	if filter != "" {
		like = "%" + filter + "%"
	}

	regionClause := ""
	args := []any{service, like, like, like, like, like, like}
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		regionClause = fmt.Sprintf(" AND r.region IN (%s)", strings.Join(holders, ","))
	}
	args = append(args, limit, offset)

	q := fmt.Sprintf(`
SELECT
  r.resource_key, r.account_id, r.partition, r.region, r.service, r.type,
  r.arn, r.primary_id, r.display_name,
  r.tags_json, r.attributes_json,
  r.collected_at, r.updated_at,
  c.est_monthly_usd, c.basis
FROM resources r
LEFT JOIN resource_costs c ON c.resource_key = r.resource_key
WHERE r.service = ?
  AND r.lifecycle_state = 'active'
  AND (
    r.display_name LIKE ?
    OR r.primary_id LIKE ?
    OR r.arn LIKE ?
    OR r.type LIKE ?
    OR r.tags_json LIKE ?
    OR r.attributes_json LIKE ?
  )%s
ORDER BY r.display_name ASC
LIMIT ? OFFSET ?
`, regionClause)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *Store) GetResourceSummariesByKeys(ctx context.Context, keys []graph.ResourceKey) (map[graph.ResourceKey]ResourceSummary, error) {
	out := map[graph.ResourceKey]ResourceSummary{}
	if len(keys) == 0 {
		return out, nil
	}

	// SQLite has a parameter limit (commonly 999). Chunk to be safe.
	const chunkSize = 400
	for i := 0; i < len(keys); i += chunkSize {
		j := i + chunkSize
		if j > len(keys) {
			j = len(keys)
		}
		chunk := keys[i:j]

		holders := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk))
		for _, k := range chunk {
			holders = append(holders, "?")
			args = append(args, string(k))
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
  AND r.lifecycle_state = 'active'
`, strings.Join(holders, ","))

		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}

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
				_ = rows.Close()
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

			out[graph.ResourceKey(keyStr)] = ResourceSummary{
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
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}

	return out, nil
}
