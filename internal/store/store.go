package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	offline bool
	path    string
}

type OpenOptions struct {
	Path    string
	Offline bool
}

func Open(opts OpenOptions) (*Store, error) {
	p, err := resolveDBPath(opts.Path)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", p)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}

	st := &Store{db: db, offline: opts.Offline, path: p}
	if err := st.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Offline() bool { return s.offline }

func (s *Store) DBPath() string { return s.path }

func resolveDBPath(in string) (string, error) {
	if in != "" {
		return in, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		// Fallback for environments without user config dir.
		tmp := os.TempDir()
		if tmp == "" {
			return "", errors.New("cannot resolve default db path")
		}
		return filepath.Join(tmp, "awscope", "awscope.sqlite"), nil
	}
	return filepath.Join(base, "awscope", "awscope.sqlite"), nil
}
