package deploy

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/store"
)

// Manager orchestrates deployment lifecycle: validate → prepare → execute
// (in background) → persist → cleanup. It fans events out to subscribers
// (SSE clients) and persists them for replay.
type Manager struct {
	backend Backend
	store   *store.Store
	secrets SecretResolver

	mu   sync.Mutex
	runs map[string]*runState
}

type runState struct {
	deployment *ir.Deployment
	subs       map[chan ir.DeploymentEvent]bool
	done       bool
}

// NewManager builds a Manager. st may be nil (deployments then run
// without persistence — used only in tests).
func NewManager(backend Backend, st *store.Store, secrets SecretResolver) *Manager {
	return &Manager{
		backend: backend,
		store:   st,
		secrets: secrets,
		runs:    map[string]*runState{},
	}
}

// Backend exposes the configured backend (for capabilities/metadata).
func (m *Manager) Backend() Backend { return m.backend }

// Start validates and prepares the deployment, then executes it in the
// background. The returned deployment is in "running" (or "pending")
// state; progress arrives via Subscribe.
func (m *Manager) Start(ctx context.Context, plan *ir.DeploymentPlan, project *ir.Project) (*ir.Deployment, error) {
	if err := m.backend.Validate(ctx, plan, project); err != nil {
		return nil, err
	}
	d := &ir.Deployment{
		ID:        plan.ID,
		Plan:      plan,
		Status:    ir.DeploymentPending,
		StartedAt: time.Now().UTC(),
	}
	rs := &runState{deployment: d, subs: map[chan ir.DeploymentEvent]bool{}}
	m.mu.Lock()
	m.runs[d.ID] = rs
	m.mu.Unlock()

	if m.store != nil {
		if err := m.store.SaveDeployment(ctx, d); err != nil {
			m.removeRun(d.ID)
			return nil, fmt.Errorf("persist deployment: %w", err)
		}
	}

	go m.run(context.Background(), plan, project, rs)
	return d, nil
}

func (m *Manager) run(ctx context.Context, plan *ir.DeploymentPlan, project *ir.Project, rs *runState) {
	d := rs.deployment
	setStatus := func(s ir.DeploymentStatus, exitCode *int) {
		m.mu.Lock()
		d.Status = s
		d.ExitCode = exitCode
		if s != ir.DeploymentRunning {
			t := time.Now().UTC()
			d.FinishedAt = &t
		}
		m.mu.Unlock()
		if m.store != nil {
			if err := m.store.SaveDeployment(ctx, d); err != nil {
				log.Printf("deploy: persist status: %v", err)
			}
		}
	}

	sink := &managerSink{m: m, rs: rs, deploymentID: d.ID}

	m.mu.Lock()
	d.Status = ir.DeploymentRunning
	m.mu.Unlock()
	if m.store != nil {
		_ = m.store.SaveDeployment(ctx, d)
	}

	prepared, err := m.backend.Prepare(ctx, plan, project, m.secrets)
	if err != nil {
		sink.Emit(finishEvent(d.ID, "prepare failed: "+err.Error()))
		setStatus(ir.DeploymentFailed, nil)
		m.finish(rs)
		return
	}
	defer func() {
		if err := m.backend.Cleanup(ctx, prepared); err != nil {
			log.Printf("deploy: cleanup: %v", err)
		}
	}()

	exitCode, execErr := m.backend.Execute(ctx, prepared, sink)
	if execErr != nil {
		sink.Emit(finishEvent(d.ID, "execution error: "+execErr.Error()))
	}

	m.mu.Lock()
	canceled := d.Status == ir.DeploymentRunning && ctx.Err() != nil && exitCode != 0
	m.mu.Unlock()

	switch {
	case canceled:
		setStatus(ir.DeploymentCanceled, &exitCode)
	case exitCode == 0 && execErr == nil:
		setStatus(ir.DeploymentSucceeded, &exitCode)
	default:
		setStatus(ir.DeploymentFailed, &exitCode)
	}
	m.finish(rs)
}

func finishEvent(deploymentID, message string) ir.DeploymentEvent {
	ev := NewEvent(deploymentID, ir.EventDeploymentFinished)
	ev.Message = message
	return ev
}

func (m *Manager) finish(rs *runState) {
	m.mu.Lock()
	rs.done = true
	for ch := range rs.subs {
		close(ch)
	}
	rs.subs = map[chan ir.DeploymentEvent]bool{}
	m.mu.Unlock()
}

func (m *Manager) removeRun(id string) {
	m.mu.Lock()
	delete(m.runs, id)
	m.mu.Unlock()
}

// Cancel requests cancellation via the backend.
func (m *Manager) Cancel(ctx context.Context, deploymentID string) error {
	return m.backend.Cancel(ctx, deploymentID)
}

// Get returns the in-memory deployment state, if managed by this process.
func (m *Manager) Get(id string) (*ir.Deployment, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.runs[id]
	if !ok {
		return nil, false
	}
	cp := *rs.deployment
	return &cp, true
}

// Subscribe registers a live event channel for a deployment. The channel
// is closed when the deployment finishes or Unsubscribe is called.
// Returns false if the deployment is unknown or already finished.
func (m *Manager) Subscribe(id string, buffer int) (<-chan ir.DeploymentEvent, func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.runs[id]
	if !ok || rs.done {
		return nil, nil, false
	}
	ch := make(chan ir.DeploymentEvent, buffer)
	rs.subs[ch] = true
	unsub := func() {
		m.mu.Lock()
		if _, ok := rs.subs[ch]; ok {
			delete(rs.subs, ch)
			close(ch)
		}
		m.mu.Unlock()
	}
	return ch, unsub, true
}

// managerSink adapts the manager to the EventSink contract: every event
// is persisted (best-effort) and broadcast to subscribers.
type managerSink struct {
	m            *Manager
	rs           *runState
	deploymentID string
}

func (s *managerSink) Emit(ev ir.DeploymentEvent) {
	if s.m.store != nil {
		if err := s.m.store.AppendDeploymentEvent(context.Background(), &ev); err != nil {
			log.Printf("deploy: persist event: %v", err)
		}
	}
	s.m.mu.Lock()
	for ch := range s.rs.subs {
		select {
		case ch <- ev:
		default: // slow consumer: drop rather than block execution
		}
	}
	s.m.mu.Unlock()
}
