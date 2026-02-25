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

func (s *Store) ListDistinctTypesByServiceAndRegions(ctx context.Context, accountID string, service string, regions []string) ([]string, error) {
	clauses := []string{"service = ?", "lifecycle_state = 'active'"}
	args := []any{service}
	accountID = strings.TrimSpace(accountID)
	if accountID != "" {
		clauses = append(clauses, "account_id = ?")
		args = append(args, accountID)
	}
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		clauses = append(clauses, fmt.Sprintf("region IN (%s)", strings.Join(holders, ",")))
	}

	where := " WHERE " + strings.Join(clauses, " AND ")

	q := fmt.Sprintf(`
SELECT DISTINCT type
FROM resources
%s
ORDER BY type ASC
`, where)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		if t != "" {
			out = append(out, t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) CountResourceSummariesByServiceTypeAndRegions(ctx context.Context, accountID string, service, typ string, regions []string, filter string) (int, error) {
	like := "%"
	if filter != "" {
		like = "%" + filter + "%"
	}

	accountID = strings.TrimSpace(accountID)
	args := []any{service, typ}
	scope := "WHERE r.service = ? AND r.type = ? AND r.lifecycle_state = 'active'"
	if accountID != "" {
		scope += " AND r.account_id = ?"
		args = append(args, accountID)
	}

	likeClause := `
AND (
  r.display_name LIKE ?
  OR r.primary_id LIKE ?
  OR r.arn LIKE ?
  OR r.type LIKE ?
  OR r.tags_json LIKE ?
  OR r.attributes_json LIKE ?
)`
	args = append(args, like, like, like, like, like, like)

	regionClause := ""
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		regionClause = fmt.Sprintf(" AND r.region IN (%s)", strings.Join(holders, ","))
	}

	q := fmt.Sprintf(`
SELECT COUNT(1)
FROM resources r
%s
%s%s
`, scope, likeClause, regionClause)

	row := s.db.QueryRowContext(ctx, q, args...)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) ListResourceSummariesByServiceTypeAndRegionsPaged(ctx context.Context, accountID string, service, typ string, regions []string, filter string, limit, offset int) ([]ResourceSummary, error) {
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

	accountID = strings.TrimSpace(accountID)
	args := []any{service, typ}
	scope := "WHERE r.service = ? AND r.type = ? AND r.lifecycle_state = 'active'"
	if accountID != "" {
		scope += " AND r.account_id = ?"
		args = append(args, accountID)
	}

	likeClause := `
AND (
  r.display_name LIKE ?
  OR r.primary_id LIKE ?
  OR r.arn LIKE ?
  OR r.type LIKE ?
  OR r.tags_json LIKE ?
  OR r.attributes_json LIKE ?
)`
	args = append(args, like, like, like, like, like, like)

	regionClause := ""
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		regionClause = fmt.Sprintf(" AND r.region IN (%s)", strings.Join(holders, ","))
	}
	args = append(args, limit, offset)

	order := "ORDER BY r.display_name ASC"
	if strings.EqualFold(strings.TrimSpace(service), "logs") && strings.EqualFold(strings.TrimSpace(typ), "logs:log-group") {
		// Prefer largest stored log groups first.
		order = "ORDER BY CAST(json_extract(r.attributes_json, '$.storedBytes') AS INTEGER) DESC, r.display_name ASC"
	} else if strings.EqualFold(strings.TrimSpace(service), "iam") && strings.EqualFold(strings.TrimSpace(typ), "iam:access-key") {
		// Prefer oldest keys first.
		order = "ORDER BY CAST(json_extract(r.attributes_json, '$.age_days') AS INTEGER) DESC, r.display_name ASC"
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
%s
%s%s
%s
LIMIT ? OFFSET ?
`, scope, likeClause, regionClause, order)

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
