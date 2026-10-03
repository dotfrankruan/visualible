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
	"github.com/dotfrankruan/visualible/internal/logging"
)

// Store wraps the application database.
type Store struct {
	db  *sql.DB
	box *secretBox

	// path is the resolved database file ("" for in-memory databases).
	path string
	// newDatabase records that this process created the database file, so
	// startup can say so loudly: an unexpectedly fresh database almost
	// always means the application is pointed at the wrong data
	// directory — not that data was lost.
	newDatabase bool
}

// Path returns the resolved database file path ("" for :memory:).
func (s *Store) Path() string { return s.path }

// NewDatabase reports whether this process created the database file.
func (s *Store) NewDatabase() bool { return s.newDatabase }

// Open opens (creating if necessary) the database at path and migrates
// the schema. keyPath locates the secret-encryption key file; pass "" to
// use an ephemeral in-memory key (tests). Use ":memory:" for tests.
//
// Durability: WAL journaling keeps committed transactions crash-safe, and
// the database is checkpointed on open and close (TRUNCATE) so the main
// .db file is self-contained at every lifecycle boundary. synchronous=FULL
// makes committed transactions survive OS crashes and power loss, which
// matters more than throughput for a control plane.
func Open(path, keyPath string) (*Store, error) {
	memory := path == ":memory:" || path == ""
	dsn := fmt.Sprintf(
		"%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)",
		path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One connection keeps :memory: databases coherent and avoids
	// SQLITE_BUSY under our low concurrency.
	db.SetMaxOpenConns(1)

	existed := true
	if !memory {
		existed = databaseExists(db)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	// Fold any surviving WAL into the main file as soon as we own the
	// database: a database that looks empty because its committed pages
	// live in a sidecar file is a support nightmare.
	checkpoint(db)

	box, err := openBox(keyPath)
	if err != nil {
		db.Close()
		return nil, err
	}
	st := &Store{db: db, box: box, path: path, newDatabase: !memory && !existed}
	if box.keyCreated {
		// A fresh key makes every stored secret undecryptable. That is
		// never silent.
		n := countRows(db, "credentials")
		if n > 0 {
			logging.Error("secret key file was missing and has been recreated",
				"keyFile", keyPath,
				"storedCredentials", n,
				"impact", "existing credentials can no longer be decrypted; re-enter them on the Targets page")
		} else {
			logging.Info("generated a new secret key file", "keyFile", keyPath)
		}
	}
	if st.newDatabase {
		logging.Warn("no existing database was found; a new one was created",
			"path", path,
			"hint", "if you expected existing projects or credentials, check the -data-dir / VISUALIBLE_DATA_DIR setting")
	} else {
		logging.Info("database opened",
			"path", path,
			"projects", countRows(db, "projects"),
			"credentials", countRows(db, "credentials"),
			"deployments", countRows(db, "deployments"))
	}
	return st, nil
}

// databaseExists reports whether the database file already contains a
// schema (i.e. this is not a fresh database).
func databaseExists(db *sql.DB) bool {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// checkpoint folds the write-ahead log into the main database file. It is
// best-effort: a failure only means the WAL keeps its pages, which SQLite
// still replays correctly on the next open.
func checkpoint(db *sql.DB) {
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		logging.Debug("wal checkpoint skipped", "err", err)
	}
}

// countRows returns the row count of a table, or 0 when it cannot be read.
func countRows(db *sql.DB, table string) int {
	var n int
	// Table names come from a fixed internal set, never from user input.
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		return 0
	}
	return n
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

// Close checkpoints the WAL and closes the database, so a clean shutdown
// always leaves a self-contained .db file (backups that copy only the
// main file then contain everything).
func (s *Store) Close() error {
	checkpoint(s.db)
	return s.db.Close()
}

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
	if err != nil {
		logging.Error("could not save project", "project", p.ID, "err", err)
		return err
	}
	logging.Debug("project saved",
		"project", p.ID, "name", p.Name,
		"playbooks", len(p.Playbooks), "inventories", len(p.Inventories),
		"bytes", len(data))
	return nil
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
	logging.Info("project deleted", "project", id)
	return nil
}
