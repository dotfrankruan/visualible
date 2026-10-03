// Package deploy abstracts deployment execution from the rest of the
// application. "Deployment" is not synonymous with SSH: backends implement
// the Backend interface (ansible over SSH today; pull/bootstrap or agent
// backends later) and the manager, API and UI only know the abstraction.
package deploy

import (
	"context"
	"time"

	"github.com/visualible/visualible/internal/ir"
)

// Capabilities describe what a backend can do so the UI can adapt.
type Capabilities struct {
	SupportsFleet        bool `json:"supportsFleet"`
	SupportsStreaming    bool `json:"supportsStreaming"`
	SupportsDryRun       bool `json:"supportsDryRun"`
	SupportsCheckMode    bool `json:"supportsCheckMode"`
	SupportsDiff         bool `json:"supportsDiff"`
	SupportsCancellation bool `json:"supportsCancellation"`
	SupportsRollback     bool `json:"supportsRollback"`
	RequiresInboundSSH   bool `json:"requiresInboundSSH"`
	RequiresAgent        bool `json:"requiresAgent"`
}

// BackendMetadata describes a backend for display.
type BackendMetadata struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SecretResolver materializes credential secrets for backends. The HTTP
// layer never sees resolved secrets; only backends receive this. The
// returned string is the credential kind (ssh_key, ssh_password, ...).
type SecretResolver interface {
	ResolveSecret(ctx context.Context, credentialID string) (kind string, secret []byte, err error)
}

// EventSink receives normalized deployment events. Raw terminal output is
// never the only execution API.
type EventSink interface {
	Emit(event ir.DeploymentEvent)
}

// PreparedDeployment is backend-owned runtime state produced by Prepare
// and consumed by Execute/Cleanup.
type PreparedDeployment struct {
	ID        string
	Workspace string // temporary directory, 0700; cleaned up after execution
	// Plan is the validated plan being executed.
	Plan *ir.DeploymentPlan
	// BackendData is opaque to the manager.
	BackendData any
}

// Backend is the deployment execution contract.
type Backend interface {
	ID() string
	Metadata() BackendMetadata
	Capabilities() Capabilities

	// Validate checks the plan against the project without side effects.
	Validate(ctx context.Context, plan *ir.DeploymentPlan, project *ir.Project) error
	// Prepare renders the deployment workspace (playbook, inventory,
	// supporting files, materialized credentials) and runs preflight
	// checks. It must not start execution.
	Prepare(ctx context.Context, plan *ir.DeploymentPlan, project *ir.Project, secrets SecretResolver) (*PreparedDeployment, error)
	// Execute runs a prepared deployment, streaming events to sink.
	// It returns when execution completes; the error is a Go-level
	// failure (spawn errors), not a failed playbook run — playbook
	// failure is reported via events and the deployment status.
	Execute(ctx context.Context, prepared *PreparedDeployment, sink EventSink) (exitCode int, err error)
	// Cancel requests cancellation of a running deployment.
	Cancel(ctx context.Context, deploymentID string) error
	// Cleanup removes the workspace, including sensitive material.
	Cleanup(ctx context.Context, prepared *PreparedDeployment) error
}

// now is injectable for tests.
var now = time.Now

// NewEvent builds a timestamped event for a deployment.
func NewEvent(deploymentID string, typ ir.DeploymentEventType) ir.DeploymentEvent {
	return ir.DeploymentEvent{
		Type:         typ,
		Timestamp:    now().UTC(),
		DeploymentID: deploymentID,
	}
}
