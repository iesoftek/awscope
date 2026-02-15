package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type ActionRunStart struct {
	ActionRunID string
	StartedAt   time.Time

	ProfileName string
	AccountID   string
	Region      string

	ResourceKey string
	ActionID    string
	Input       map[string]any
}

func (s *Store) StartActionRun(ctx context.Context, in ActionRunStart) error {
	if in.StartedAt.IsZero() {
		in.StartedAt = time.Now().UTC()
	}
	if in.Input == nil {
		in.Input = map[string]any{}
	}
	inputJSON, err := json.Marshal(in.Input)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO action_runs(
  action_run_id, started_at, ended_at,
  profile_name, account_id, region,
  resource_key, action_id, input_json, result_json, status
) VALUES (
  ?, ?, NULL,
  ?, ?, ?,
  ?, ?, ?, '{}', 'RUNNING'
)
`, in.ActionRunID, in.StartedAt.Format(time.RFC3339Nano),
		in.ProfileName, nullIfEmpty(in.AccountID), nullIfEmpty(in.Region),
		in.ResourceKey, in.ActionID, string(inputJSON),
	)
	return err
}

type ActionRunFinish struct {
	ActionRunID string
	EndedAt     time.Time
	Status      string
	Result      map[string]any
}

func (s *Store) FinishActionRun(ctx context.Context, in ActionRunFinish) error {
	if in.EndedAt.IsZero() {
		in.EndedAt = time.Now().UTC()
	}
	if in.Result == nil {
		in.Result = map[string]any{}
	}
	resultJSON, err := json.Marshal(in.Result)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
UPDATE action_runs
SET ended_at = ?, result_json = ?, status = ?
WHERE action_run_id = ?
`, in.EndedAt.Format(time.RFC3339Nano), string(resultJSON), in.Status, in.ActionRunID)
	return err
}

func (s *Store) GetActionRunStatus(ctx context.Context, actionRunID string) (string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT status FROM action_runs WHERE action_run_id = ?`, actionRunID)
	var status sql.NullString
	if err := row.Scan(&status); err != nil {
		return "", err
	}
	return status.String, nil
}
