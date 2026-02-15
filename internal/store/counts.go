package store

import (
	"context"
	"fmt"
	"strings"
)

type ServiceCount struct {
	Service string
	Count   int
}

type TypeCount struct {
	Type  string
	Count int
}

func (s *Store) ListServiceCountsByRegions(ctx context.Context, accountID string, regions []string) ([]ServiceCount, error) {
	clauses := []string{}
	args := []any{}

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

	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	q := fmt.Sprintf(`
SELECT service, COUNT(1)
FROM resources%s
GROUP BY service
ORDER BY service ASC
`, where)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ServiceCount
	for rows.Next() {
		var sc ServiceCount
		if err := rows.Scan(&sc.Service, &sc.Count); err != nil {
			return nil, err
		}
		if sc.Service != "" {
			out = append(out, sc)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListTypeCountsByServiceAndRegions(ctx context.Context, accountID string, service string, regions []string) ([]TypeCount, error) {
	if strings.TrimSpace(service) == "" {
		return nil, nil
	}

	clauses := []string{"service = ?"}
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
SELECT type, COUNT(1)
FROM resources
%s
GROUP BY type
ORDER BY type ASC
`, where)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TypeCount
	for rows.Next() {
		var tc TypeCount
		if err := rows.Scan(&tc.Type, &tc.Count); err != nil {
			return nil, err
		}
		if tc.Type != "" {
			out = append(out, tc)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
