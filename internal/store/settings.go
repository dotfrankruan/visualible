package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const settingsSchema = `
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// Settings is the typed application configuration persisted locally.
// Secrets are never stored here — only credential references into the
// encrypted credential store, so settings responses are safe by
// construction.
type Settings struct {
	// Ansible path overrides; empty means PATH lookup.
	AnsiblePath         string `json:"ansiblePath,omitempty"`
	AnsibleDocPath      string `json:"ansibleDocPath,omitempty"`
	AnsiblePlaybookPath string `json:"ansiblePlaybookPath,omitempty"`
	// ExtraEnv is appended to the environment of Ansible subprocesses.
	ExtraEnv map[string]string `json:"extraEnv,omitempty"`

	AI AISettings `json:"ai"`
	S3 S3Settings `json:"s3"`

	Appearance AppearanceSettings `json:"appearance"`
}

// AISettings configures an OpenAI-compatible provider.
type AISettings struct {
	Endpoint     string            `json:"endpoint,omitempty"` // e.g. http://localhost:11434/v1
	Model        string            `json:"model,omitempty"`
	APIKeyCredID string            `json:"apiKeyCredId,omitempty"` // credential reference
	Headers      map[string]string `json:"headers,omitempty"`
	// Temperature is optional; nil means "let the provider decide".
	Temperature *float64 `json:"temperature,omitempty"`
}

// S3Settings configures S3-compatible artifact storage. The backend is
// deferred; only the configuration model exists in v0.1.
type S3Settings struct {
	Endpoint        string `json:"endpoint,omitempty"`
	Region          string `json:"region,omitempty"`
	Bucket          string `json:"bucket,omitempty"`
	Prefix          string `json:"prefix,omitempty"`
	SecretKeyCredID string `json:"secretKeyCredId,omitempty"`
}

// AppearanceSettings holds UI preferences.
type AppearanceSettings struct {
	Theme string `json:"theme,omitempty"` // "dark" (default) | "light"
}

const settingsKey = "app"

// GetSettings loads settings; absent settings yield zero values.
func (s *Store) GetSettings(ctx context.Context) (*Settings, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return &Settings{}, nil
	}
	if err != nil {
		return nil, err
	}
	var st Settings
	if err := json.Unmarshal([]byte(data), &st); err != nil {
		return nil, fmt.Errorf("corrupt settings: %w", err)
	}
	return &st, nil
}

// SaveSettings persists settings (full replace).
func (s *Store) SaveSettings(ctx context.Context, st *Settings) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKey, string(data))
	return err
}
