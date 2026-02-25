package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type ScanScope struct {
	Service string
	Regions []string
}

func (s *Store) MarkResourcesStaleNotSeenInScopes(ctx context.Context, accountID, scanID string, scopes []ScanScope, missingSince time.Time) (int, error) {
	accountID = strings.TrimSpace(accountID)
	scanID = strings.TrimSpace(scanID)
	if accountID == "" || scanID == "" || len(scopes) == 0 {
		return 0, nil
	}
	if missingSince.IsZero() {
		missingSince = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	total := 0
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	missingSinceRFC := missingSince.UTC().Format(time.RFC3339Nano)

	for _, scope := range scopes {
		service := strings.TrimSpace(scope.Service)
		if service == "" {
			continue
		}

		seenRegion := map[string]struct{}{}
		regions := make([]string, 0, len(scope.Regions))
		for _, region := range scope.Regions {
			region = strings.TrimSpace(region)
			if region == "" {
				continue
			}
			if _, ok := seenRegion[region]; ok {
				continue
			}
			seenRegion[region] = struct{}{}
			regions = append(regions, region)
		}
		if len(regions) == 0 {
			continue
		}

		holders := make([]string, 0, len(regions))
		args := []any{missingSinceRFC, updatedAt, accountID, service, scanID}
		for _, region := range regions {
			holders = append(holders, "?")
			args = append(args, region)
		}

		q := fmt.Sprintf(`
UPDATE resources
SET lifecycle_state='stale', missing_since=?, updated_at=?
WHERE account_id = ?
  AND service = ?
  AND lifecycle_state = 'active'
  AND (last_seen_scan_id IS NULL OR last_seen_scan_id <> ?)
  AND region IN (%s)
`, strings.Join(holders, ","))
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) PurgeStaleResources(ctx context.Context, accountID string, olderThan *time.Time) (deletedResources int, deletedEdges int, deletedCosts int, err error) {
	accountID = strings.TrimSpace(accountID)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS tmp_purge_stale_keys(resource_key TEXT PRIMARY KEY)`); err != nil {
		return 0, 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tmp_purge_stale_keys`); err != nil {
		return 0, 0, 0, err
	}

	clauses := []string{"lifecycle_state = 'stale'"}
	args := []any{}
	if accountID != "" {
		clauses = append(clauses, "account_id = ?")
		args = append(args, accountID)
	}
	if olderThan != nil {
		clauses = append(clauses, "missing_since IS NOT NULL", "missing_since <= ?")
		args = append(args, olderThan.UTC().Format(time.RFC3339Nano))
	}

	insertQ := fmt.Sprintf(`
INSERT INTO tmp_purge_stale_keys(resource_key)
SELECT resource_key
FROM resources
WHERE %s
`, strings.Join(clauses, " AND "))
	if _, err := tx.ExecContext(ctx, insertQ, args...); err != nil {
		return 0, 0, 0, err
	}

	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM tmp_purge_stale_keys`).Scan(&deletedResources); err != nil {
		return 0, 0, 0, err
	}
	if deletedResources == 0 {
		if err := tx.Commit(); err != nil {
			return 0, 0, 0, err
		}
		return 0, 0, 0, nil
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM resource_costs WHERE resource_key IN (SELECT resource_key FROM tmp_purge_stale_keys)`)
	if err != nil {
		return 0, 0, 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		deletedCosts = int(n)
	}

	res, err = tx.ExecContext(ctx, `DELETE FROM edges WHERE from_key IN (SELECT resource_key FROM tmp_purge_stale_keys) OR to_key IN (SELECT resource_key FROM tmp_purge_stale_keys)`)
	if err != nil {
		return 0, 0, 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		deletedEdges = int(n)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE resource_key IN (SELECT resource_key FROM tmp_purge_stale_keys)`); err != nil {
		return 0, 0, 0, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM tmp_purge_stale_keys`); err != nil {
		return 0, 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	return deletedResources, deletedEdges, deletedCosts, nil
}
