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

type ECSDrillScope struct {
	Level       string // "services" | "tasks"
	ClusterKey  graph.ResourceKey
	ClusterArn  string
	ServiceKey  graph.ResourceKey
	ServiceName string
	Region      string
}

func (s *Store) CountECSDrillResourceSummaries(ctx context.Context, accountID string, typ string, regions []string, filter string, scope ECSDrillScope) (int, error) {
	like := "%"
	if filter != "" {
		like = "%" + filter + "%"
	}

	accountID = strings.TrimSpace(accountID)
	clauses := []string{
		"r.service = 'ecs'",
		"r.type = ?",
		"r.lifecycle_state = 'active'",
	}
	args := []any{typ}
	if accountID != "" {
		clauses = append(clauses, "r.account_id = ?")
		args = append(args, accountID)
	}
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		clauses = append(clauses, fmt.Sprintf("r.region IN (%s)", strings.Join(holders, ",")))
	}
	if region := strings.TrimSpace(scope.Region); region != "" {
		clauses = append(clauses, "r.region = ?")
		args = append(args, region)
	}

	clauses = append(clauses, `(r.display_name LIKE ? OR r.primary_id LIKE ? OR r.arn LIKE ? OR r.type LIKE ? OR r.tags_json LIKE ? OR r.attributes_json LIKE ?)`)
	args = append(args, like, like, like, like, like, like)

	scopeClause, scopeArgs, err := ecsDrillScopeClause(scope, typ)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(scopeClause) != "" {
		clauses = append(clauses, scopeClause)
		args = append(args, scopeArgs...)
	}

	q := fmt.Sprintf(`
SELECT COUNT(1)
FROM resources r
WHERE %s
`, strings.Join(clauses, " AND "))

	row := s.db.QueryRowContext(ctx, q, args...)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) ListECSDrillResourceSummariesPaged(ctx context.Context, accountID string, typ string, regions []string, filter string, scope ECSDrillScope, limit, offset int) ([]ResourceSummary, error) {
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
	clauses := []string{
		"r.service = 'ecs'",
		"r.type = ?",
		"r.lifecycle_state = 'active'",
	}
	args := []any{typ}
	if accountID != "" {
		clauses = append(clauses, "r.account_id = ?")
		args = append(args, accountID)
	}
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		clauses = append(clauses, fmt.Sprintf("r.region IN (%s)", strings.Join(holders, ",")))
	}
	if region := strings.TrimSpace(scope.Region); region != "" {
		clauses = append(clauses, "r.region = ?")
		args = append(args, region)
	}

	clauses = append(clauses, `(r.display_name LIKE ? OR r.primary_id LIKE ? OR r.arn LIKE ? OR r.type LIKE ? OR r.tags_json LIKE ? OR r.attributes_json LIKE ?)`)
	args = append(args, like, like, like, like, like, like)

	scopeClause, scopeArgs, err := ecsDrillScopeClause(scope, typ)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(scopeClause) != "" {
		clauses = append(clauses, scopeClause)
		args = append(args, scopeArgs...)
	}

	order := "ORDER BY r.display_name ASC"
	switch strings.TrimSpace(scope.Level) {
	case "services":
		order = "ORDER BY CAST(COALESCE(json_extract(r.attributes_json, '$.runningCount'), 0) AS INTEGER) DESC, r.display_name ASC"
	case "tasks":
		order = "ORDER BY COALESCE(json_extract(r.attributes_json, '$.created_at'), '') DESC, r.display_name ASC"
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
WHERE %s
%s
LIMIT ? OFFSET ?
`, strings.Join(clauses, " AND "), order)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ResourceSummary
	for rows.Next() {
		var (
			keyStr      string
			rowAccount  string
			partition   string
			region      string
			service     string
			rowType     string
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
			&keyStr, &rowAccount, &partition, &region, &service, &rowType,
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
			AccountID:     rowAccount,
			Partition:     partition,
			Region:        region,
			Service:       service,
			Type:          rowType,
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

func ecsDrillScopeClause(scope ECSDrillScope, typ string) (string, []any, error) {
	switch strings.TrimSpace(scope.Level) {
	case "services":
		if typ != "ecs:service" {
			return "", nil, fmt.Errorf("ecs services drill requires type ecs:service")
		}
		if strings.TrimSpace(string(scope.ClusterKey)) == "" {
			return "", nil, fmt.Errorf("ecs services drill requires cluster key")
		}
		return "EXISTS (SELECT 1 FROM edges e WHERE e.from_key = r.resource_key AND e.to_key = ? AND e.kind = 'member-of')",
			[]any{string(scope.ClusterKey)},
			nil

	case "tasks":
		if typ != "ecs:task" {
			return "", nil, fmt.Errorf("ecs tasks drill requires type ecs:task")
		}
		serviceKey := strings.TrimSpace(string(scope.ServiceKey))
		serviceName := strings.TrimSpace(scope.ServiceName)
		clusterArn := strings.TrimSpace(scope.ClusterArn)
		if serviceKey == "" && serviceName == "" {
			return "", nil, fmt.Errorf("ecs tasks drill requires service key or service name")
		}

		var (
			parts []string
			args  []any
		)
		if serviceKey != "" {
			parts = append(parts, "EXISTS (SELECT 1 FROM edges e WHERE e.from_key = r.resource_key AND e.to_key = ? AND e.kind = 'belongs-to')")
			args = append(args, serviceKey)
		}
		if serviceName != "" {
			fallback := "json_extract(r.attributes_json, '$.serviceName') = ?"
			fallbackArgs := []any{serviceName}
			if clusterArn != "" {
				fallback += " AND json_extract(r.attributes_json, '$.clusterArn') = ?"
				fallbackArgs = append(fallbackArgs, clusterArn)
			}
			parts = append(parts, "("+fallback+")")
			args = append(args, fallbackArgs...)
		}
		return "(" + strings.Join(parts, " OR ") + ")", args, nil

	default:
		return "", nil, fmt.Errorf("unsupported ecs drill level %q", scope.Level)
	}
}
