package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type ScanRunMeta struct {
	ScanID      string
	ProfileName string
	Status      string
	EndedAt     time.Time
	ScopeJSON   string
	ProviderIDs []string
	Regions     []string
}

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

func (s *Store) GetLatestSuccessfulScanRunByProfile(ctx context.Context, profile string) (ScanRunMeta, bool, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return ScanRunMeta{}, false, nil
	}

	row := s.db.QueryRowContext(ctx, `
SELECT scan_id, profile_name, status, ended_at, scope_json
FROM scan_runs
WHERE profile_name = ? AND status = 'success'
ORDER BY ended_at DESC
LIMIT 1
`, profile)

	var (
		out      ScanRunMeta
		endedAt  sql.NullString
		scopeRaw string
	)
	if err := row.Scan(&out.ScanID, &out.ProfileName, &out.Status, &endedAt, &scopeRaw); err != nil {
		if err == sql.ErrNoRows {
			return ScanRunMeta{}, false, nil
		}
		return ScanRunMeta{}, false, err
	}
	out.ScopeJSON = scopeRaw

	if endedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, endedAt.String); err == nil {
			out.EndedAt = t
		}
	}

	var scope struct {
		ProviderIDs []string `json:"provider_ids"`
		Regions     []string `json:"regions"`
	}
	if strings.TrimSpace(scopeRaw) != "" {
		_ = json.Unmarshal([]byte(scopeRaw), &scope)
	}
	out.ProviderIDs = normalizeStringList(scope.ProviderIDs)
	out.Regions = normalizeStringList(scope.Regions)
	return out, true, nil
}

func normalizeStringList(in []string) []string {
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
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
