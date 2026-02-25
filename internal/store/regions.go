package store

import (
	"context"
	"strings"
)

type RegionCount struct {
	Region string
	Count  int
}

func (s *Store) ListRegionCountsByService(ctx context.Context, accountID string, service string) ([]RegionCount, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, nil
	}

	accountID = strings.TrimSpace(accountID)
	q := `
SELECT region, COUNT(1)
FROM resources
WHERE service = ?
  AND lifecycle_state = 'active'
GROUP BY region
ORDER BY region
`
	args := []any{service}
	if accountID != "" {
		q = `
SELECT region, COUNT(1)
FROM resources
WHERE service = ? AND account_id = ?
  AND lifecycle_state = 'active'
GROUP BY region
ORDER BY region
`
		args = append(args, accountID)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RegionCount
	for rows.Next() {
		var r RegionCount
		if err := rows.Scan(&r.Region, &r.Count); err != nil {
			return nil, err
		}
		if strings.TrimSpace(r.Region) != "" {
			out = append(out, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListRegionCountsByServiceType(ctx context.Context, accountID string, service, typ string) ([]RegionCount, error) {
	service = strings.TrimSpace(service)
	typ = strings.TrimSpace(typ)
	if service == "" || typ == "" {
		return nil, nil
	}

	accountID = strings.TrimSpace(accountID)
	q := `
SELECT region, COUNT(1)
FROM resources
WHERE service = ? AND type = ?
  AND lifecycle_state = 'active'
GROUP BY region
ORDER BY region
`
	args := []any{service, typ}
	if accountID != "" {
		q = `
SELECT region, COUNT(1)
FROM resources
WHERE service = ? AND type = ? AND account_id = ?
  AND lifecycle_state = 'active'
GROUP BY region
ORDER BY region
`
		args = append(args, accountID)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RegionCount
	for rows.Next() {
		var r RegionCount
		if err := rows.Scan(&r.Region, &r.Count); err != nil {
			return nil, err
		}
		if strings.TrimSpace(r.Region) != "" {
			out = append(out, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
