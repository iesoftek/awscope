package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"awscope/internal/graph"
)

type ResourceLookup struct {
	Key         graph.ResourceKey
	Region      string
	Service     string
	Type        string
	PrimaryID   string
	Arn         string
	DisplayName string
}

func (s *Store) ListResourceLookupsByAccountAndRegions(ctx context.Context, accountID string, regions []string) ([]ResourceLookup, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, nil
	}

	args := []any{accountID}
	scope := "WHERE account_id = ? AND lifecycle_state = 'active'"
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			holders = append(holders, "?")
			args = append(args, r)
		}
		if len(holders) > 0 {
			scope += fmt.Sprintf(" AND region IN (%s)", strings.Join(holders, ","))
		}
	}

	q := fmt.Sprintf(`
SELECT resource_key, region, service, type, primary_id, arn, display_name
FROM resources
%s
`, scope)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ResourceLookup
	for rows.Next() {
		var (
			k           string
			region      string
			service     string
			typ         string
			primaryID   string
			arn         sql.NullString
			displayName string
		)
		if err := rows.Scan(&k, &region, &service, &typ, &primaryID, &arn, &displayName); err != nil {
			return nil, err
		}
		out = append(out, ResourceLookup{
			Key:         graph.ResourceKey(k),
			Region:      region,
			Service:     service,
			Type:        typ,
			PrimaryID:   primaryID,
			Arn:         arn.String,
			DisplayName: displayName,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
