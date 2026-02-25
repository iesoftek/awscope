package store

import (
	"context"
	"strings"
	"time"
)

func (s *Store) StartScanRun(ctx context.Context, scanID, profileName string, scopeJSON string, startedAt time.Time) error {
	scanID = strings.TrimSpace(scanID)
	if scanID == "" {
		return nil
	}
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = "default"
	}
	if strings.TrimSpace(scopeJSON) == "" {
		scopeJSON = "{}"
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO scan_runs(scan_id, started_at, ended_at, profile_name, scope_json, status)
VALUES(?, ?, NULL, ?, ?, 'running')
ON CONFLICT(scan_id) DO UPDATE SET
  started_at=excluded.started_at,
  ended_at=NULL,
  profile_name=excluded.profile_name,
  scope_json=excluded.scope_json,
  status='running'
`, scanID, startedAt.UTC().Format(time.RFC3339Nano), profileName, scopeJSON)
	return err
}

func (s *Store) FinishScanRun(ctx context.Context, scanID, status string, endedAt time.Time) error {
	scanID = strings.TrimSpace(scanID)
	if scanID == "" {
		return nil
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "success"
	}
	if endedAt.IsZero() {
		endedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE scan_runs
SET ended_at = ?, status = ?
WHERE scan_id = ?
`, endedAt.UTC().Format(time.RFC3339Nano), status, scanID)
	return err
}
