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

func (s *Store) ListResourceNodesByAccountAndScope(ctx context.Context, accountID string, regions []string, services []string) ([]graph.ResourceNode, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, nil
	}

	args := []any{accountID}
	clauses := []string{
		"account_id = ?",
		"lifecycle_state = 'active'",
	}

	if rs := normalizeScopeList(regions); len(rs) > 0 {
		holders := make([]string, 0, len(rs))
		for _, r := range rs {
			holders = append(holders, "?")
			args = append(args, r)
		}
		clauses = append(clauses, fmt.Sprintf("region IN (%s)", strings.Join(holders, ",")))
	}

	if ss := normalizeScopeList(services); len(ss) > 0 {
		holders := make([]string, 0, len(ss))
		for _, svc := range ss {
			holders = append(holders, "?")
			args = append(args, svc)
		}
		clauses = append(clauses, fmt.Sprintf("service IN (%s)", strings.Join(holders, ",")))
	}

	q := fmt.Sprintf(`
SELECT resource_key, service, type, arn, primary_id, display_name, tags_json, attributes_json, raw_json, collected_at
FROM resources
WHERE %s
ORDER BY service ASC, type ASC, display_name ASC
`, strings.Join(clauses, " AND "))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []graph.ResourceNode
	for rows.Next() {
		var (
			keyStr      string
			service     string
			typ         string
			arn         sql.NullString
			primaryID   string
			displayName string
			tagsJSON    string
			attrsJSON   string
			rawJSON     string
			collectedAt sql.NullString
		)
		if err := rows.Scan(&keyStr, &service, &typ, &arn, &primaryID, &displayName, &tagsJSON, &attrsJSON, &rawJSON, &collectedAt); err != nil {
			return nil, err
		}

		tags := map[string]string{}
		_ = json.Unmarshal([]byte(tagsJSON), &tags)
		attrs := map[string]any{}
		_ = json.Unmarshal([]byte(attrsJSON), &attrs)

		var collected time.Time
		if collectedAt.Valid {
			if t, err := time.Parse(time.RFC3339Nano, collectedAt.String); err == nil {
				collected = t
			}
		}

		out = append(out, graph.ResourceNode{
			Key:         graph.ResourceKey(keyStr),
			DisplayName: displayName,
			Service:     service,
			Type:        typ,
			Arn:         arn.String,
			PrimaryID:   primaryID,
			Tags:        tags,
			Attributes:  attrs,
			Raw:         json.RawMessage(rawJSON),
			CollectedAt: collected,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeScopeList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		v = strings.ToLower(v)
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
