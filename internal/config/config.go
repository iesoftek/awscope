package config

import (
	"os"
	"path/filepath"
)

// Config is intentionally minimal in bootstrap; we will expand per REQUIREMENTS.md.
type Config struct {
	DefaultProfile string `yaml:"default_profile"`
	SQLitePath     string `yaml:"sqlite_path"`
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "awscope", "config.yaml"), nil
}

