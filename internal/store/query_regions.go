package store

import (
	"context"
	"strings"
)

func (s *Store) ListDistinctRegions(ctx context.Context, accountID string) ([]string, error) {
	accountID = strings.TrimSpace(accountID)
	q := `SELECT DISTINCT region FROM resources ORDER BY region`
	args := []any{}
	if accountID != "" {
		q = `SELECT DISTINCT region FROM resources WHERE account_id = ? ORDER BY region`
		args = append(args, accountID)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		if r != "" {
			out = append(out, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
