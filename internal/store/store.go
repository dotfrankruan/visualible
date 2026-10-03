// Package store persists application state in SQLite (pure Go driver,
// no cgo — single-binary distribution stays trivial). Projects are
// stored as their canonical IR JSON; the IR remains the source of
// truth and SQLite is a document store for it, not a relational
// re-modeling. Other state (settings, credentials, deployments) gets
// its own small typed tables in later phases.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dotfrankruan/visualible/internal/ir"
)

// Store wraps the application database.
type Store struct {
	db  *sql.DB
	box *secretBox
}

// Open opens (creating if necessary) the database at path and migrates
// the schema. keyPath locates the secret-encryption key file; pass "" to
// use an ephemeral in-memory key (tests). Use ":memory:" for tests.
func Open(path, keyPath string) (*Store, error) {
	// foreign_keys on; busy_timeout so a crashed peer cannot wedge us.
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One connection keeps :memory: databases coherent and avoids
	// SQLITE_BUSY under our low concurrency.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	box, err := openBox(keyPath)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, box: box}, nil
}

func openBox(keyPath string) (*secretBox, error) {
	if keyPath == "" {
		// Ephemeral key: secrets are unreadable after Close. Tests only.
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		aead, err := newGCM(key)
		if err != nil {
			return nil, err
		}
		return &secretBox{aead: aead}, nil
	}
	return openSecretBox(keyPath)
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    data       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_projects_name ON projects(name);
` + credentialsSchema + deploymentsSchema + settingsSchema
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// ErrNotFound is returned when an entity does not exist.
var ErrNotFound = errors.New("not found")

// ProjectMeta is the lightweight listing view of a project.
type ProjectMeta struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

const timeFormat = time.RFC3339Nano

// ListProjects returns metadata for all projects, newest first.
func (s *Store) ListProjects(ctx context.Context) ([]ProjectMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, data, created_at, updated_at FROM projects ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectMeta
	for rows.Next() {
		var m ProjectMeta
		var data, created, updated string
		if err := rows.Scan(&m.ID, &m.Name, &data, &created, &updated); err != nil {
			return nil, err
		}
		// Description lives inside the IR document; extract cheaply.
		var doc struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal([]byte(data), &doc)
		m.Description = doc.Description
		m.CreatedAt, _ = time.Parse(timeFormat, created)
		m.UpdatedAt, _ = time.Parse(timeFormat, updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetProject loads a full project by ID.
func (s *Store) GetProject(ctx context.Context, id string) (*ir.Project, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM projects WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("project %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var p ir.Project
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, fmt.Errorf("corrupt project %s: %w", id, err)
	}
	return &p, nil
}

// SaveProject inserts or replaces a project. The project must pass IR
// validation; CreatedAt is preserved on update, UpdatedAt is refreshed.
func (s *Store) SaveProject(ctx context.Context, p *ir.Project) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("refusing to persist invalid project: %w", err)
	}
	now := time.Now().UTC()
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM projects WHERE id = ?`, p.ID).Scan(&created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		p.CreatedAt = now
	case err != nil:
		return err
	default:
		p.CreatedAt, _ = time.Parse(timeFormat, created)
	}
	p.UpdatedAt = now

	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode project: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, data, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, data = excluded.data,
		     updated_at = excluded.updated_at`,
		p.ID, p.Name, string(data), p.CreatedAt.Format(timeFormat), p.UpdatedAt.Format(timeFormat))
	return err
}

// DeleteProject removes a project.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %s: %w", id, ErrNotFound)
	}
	return nil
}
