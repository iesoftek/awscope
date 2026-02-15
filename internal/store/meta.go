package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type LastUsedProfile struct {
	ProfileName string
	AccountID   string
	Partition   string
	LastUsedAt  time.Time
}

func (s *Store) UpsertAccountSeen(ctx context.Context, accountID, partition string, seenAt time.Time) error {
	if seenAt.IsZero() {
		seenAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO accounts(account_id, partition, last_seen_at)
VALUES(?, ?, ?)
ON CONFLICT(account_id) DO UPDATE SET
  partition=excluded.partition,
  last_seen_at=excluded.last_seen_at
`, accountID, partition, seenAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) UpsertProfileUsed(ctx context.Context, profileName, accountID string, usedAt time.Time) error {
	if usedAt.IsZero() {
		usedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO profiles(profile_name, account_id, role_arn, last_used_at)
VALUES(?, ?, NULL, ?)
ON CONFLICT(profile_name) DO UPDATE SET
  account_id=excluded.account_id,
  last_used_at=excluded.last_used_at
`, profileName, accountID, usedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetLastUsedProfile(ctx context.Context) (LastUsedProfile, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT p.profile_name, p.account_id, a.partition, p.last_used_at
FROM profiles p
LEFT JOIN accounts a ON a.account_id = p.account_id
WHERE p.last_used_at IS NOT NULL
ORDER BY p.last_used_at DESC
LIMIT 1
`)

	var (
		profileName string
		accountID   sql.NullString
		partition   sql.NullString
		lastUsedAt  sql.NullString
	)
	if err := row.Scan(&profileName, &accountID, &partition, &lastUsedAt); err != nil {
		return LastUsedProfile{}, err
	}

	var t time.Time
	if lastUsedAt.Valid {
		tt, err := time.Parse(time.RFC3339Nano, lastUsedAt.String)
		if err == nil {
			t = tt
		}
	}

	return LastUsedProfile{
		ProfileName: profileName,
		AccountID:   accountID.String,
		Partition:   partition.String,
		LastUsedAt:  t,
	}, nil
}

func (s *Store) GetProfile(ctx context.Context, profileName string) (LastUsedProfile, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT p.profile_name, p.account_id, a.partition, p.last_used_at
FROM profiles p
LEFT JOIN accounts a ON a.account_id = p.account_id
WHERE p.profile_name = ?
LIMIT 1
`, profileName)

	var (
		outProfile string
		accountID  sql.NullString
		partition  sql.NullString
		lastUsedAt sql.NullString
	)
	if err := row.Scan(&outProfile, &accountID, &partition, &lastUsedAt); err != nil {
		return LastUsedProfile{}, err
	}

	var t time.Time
	if lastUsedAt.Valid {
		tt, err := time.Parse(time.RFC3339Nano, lastUsedAt.String)
		if err == nil {
			t = tt
		}
	}

	return LastUsedProfile{
		ProfileName: outProfile,
		AccountID:   accountID.String,
		Partition:   partition.String,
		LastUsedAt:  t,
	}, nil
}

func (s *Store) LookupProfile(ctx context.Context, profileName string) (LastUsedProfile, bool, error) {
	m, err := s.GetProfile(ctx, profileName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LastUsedProfile{}, false, nil
		}
		return LastUsedProfile{}, false, err
	}
	return m, true, nil
}
