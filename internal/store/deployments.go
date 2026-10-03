package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/visualible/visualible/internal/ir"
)

const deploymentsSchema = `
CREATE TABLE IF NOT EXISTS deployments (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    plan        TEXT NOT NULL,
    status      TEXT NOT NULL,
    exit_code   INTEGER,
    started_at  TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_deployments_project ON deployments(project_id, started_at DESC);

CREATE TABLE IF NOT EXISTS deployment_events (
    rowid_seq     INTEGER PRIMARY KEY AUTOINCREMENT,
    deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    type          TEXT NOT NULL,
    data          TEXT NOT NULL,
    ts            TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deployment_events ON deployment_events(deployment_id, rowid_seq);
`

// SaveDeployment upserts a deployment record.
func (s *Store) SaveDeployment(ctx context.Context, d *ir.Deployment) error {
	planJSON, err := json.Marshal(d.Plan)
	if err != nil {
		return err
	}
	var finished *string
	if d.FinishedAt != nil {
		f := d.FinishedAt.Format(timeFormat)
		finished = &f
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO deployments (id, project_id, plan, status, exit_code, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET status = excluded.status,
		     exit_code = excluded.exit_code, finished_at = excluded.finished_at`,
		d.ID, d.Plan.ProjectID, string(planJSON), string(d.Status), d.ExitCode,
		d.StartedAt.Format(timeFormat), finished)
	return err
}

// AppendDeploymentEvent persists one event for replay.
func (s *Store) AppendDeploymentEvent(ctx context.Context, ev *ir.DeploymentEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO deployment_events (deployment_id, type, data, ts) VALUES (?, ?, ?, ?)`,
		ev.DeploymentID, string(ev.Type), string(data), ev.Timestamp.Format(timeFormat))
	return err
}

// DeploymentEvents returns all events of a deployment in order.
func (s *Store) DeploymentEvents(ctx context.Context, deploymentID string) ([]ir.DeploymentEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT data FROM deployment_events WHERE deployment_id = ? ORDER BY rowid_seq`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ir.DeploymentEvent{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var ev ir.DeploymentEvent
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			out = append(out, ev)
		}
	}
	return out, rows.Err()
}

// GetDeployment loads one deployment record.
func (s *Store) GetDeployment(ctx context.Context, id string) (*ir.Deployment, error) {
	var d ir.Deployment
	var planJSON, status, started string
	var finished *string
	var exitCode *int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, plan, status, exit_code, started_at, finished_at FROM deployments WHERE id = ?`, id).
		Scan(&d.ID, &planJSON, &status, &exitCode, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("deployment %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var plan ir.DeploymentPlan
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return nil, fmt.Errorf("corrupt deployment %s plan: %w", id, err)
	}
	d.Plan = &plan
	d.Status = ir.DeploymentStatus(status)
	d.ExitCode = exitCode
	d.StartedAt, _ = time.Parse(timeFormat, started)
	if finished != nil {
		t, _ := time.Parse(timeFormat, *finished)
		d.FinishedAt = &t
	}
	return &d, nil
}

// ListDeployments returns deployments, newest first.
func (s *Store) ListDeployments(ctx context.Context, limit int) ([]ir.Deployment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM deployments ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []ir.Deployment{}
	for _, id := range ids {
		d, err := s.GetDeployment(ctx, id)
		if err == nil {
			out = append(out, *d)
		}
	}
	return out, nil
}
