// Package ir defines Visualible's internal representation of Ansible
// projects. The IR is the canonical source of truth for everything the
// visual editor creates; visual graph state and rendered Ansible YAML are
// both derived from it, never the other way around.
package ir

import (
	"time"
)

// Project is the top-level container managed by Visualible.
type Project struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Playbooks   []*Playbook  `json:"playbooks"`
	Inventories []*Inventory `json:"inventories"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

// Playbook is an ordered collection of plays, rendered as a single
// Ansible playbook YAML document (a YAML list of plays).
type Playbook struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Plays []*Play `json:"plays"`
}

// Play maps to an Ansible play.
type Play struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Hosts     string          `json:"hosts"`
	Become    bool            `json:"become,omitempty"`
	Vars      map[string]any  `json:"vars,omitempty"`
	Roles     []RoleReference `json:"roles,omitempty"`
	PreTasks  []*Task         `json:"preTasks,omitempty"`
	Tasks     []*Task         `json:"tasks"`
	PostTasks []*Task         `json:"postTasks,omitempty"`
	Handlers  []*Task         `json:"handlers,omitempty"`
	Tags      []string        `json:"tags,omitempty"`
}

// RoleReference is a reference to an Ansible role. Roles themselves are
// resolved by Ansible at execution time; Visualible only references them.
type RoleReference struct {
	Role string         `json:"role"`
	Vars map[string]any `json:"vars,omitempty"`
	Tags []string       `json:"tags,omitempty"`
}

// Task maps to an Ansible task. It is deliberately extensible: fields the
// v0.1 UI does not expose yet (Block/Rescue/Always, includes, retries) are
// part of the model so imported content and future renderers have a place
// to live without schema changes.
type Task struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Module string         `json:"module"` // FQCN, e.g. ansible.builtin.apt
	Args   map[string]any `json:"args,omitempty"`

	When        string            `json:"when,omitempty"`
	Loop        any               `json:"loop,omitempty"`
	Register    string            `json:"register,omitempty"`
	Notify      []string          `json:"notify,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Become      *bool             `json:"become,omitempty"`
	DelegateTo  string            `json:"delegateTo,omitempty"`
	RunOnce     bool              `json:"runOnce,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	ChangedWhen string            `json:"changedWhen,omitempty"`
	FailedWhen  string            `json:"failedWhen,omitempty"`

	// Reserved for future phases. Not rendered or editable in v0.1, but
	// carried so parsing/validation can detect and report them rather than
	// silently dropping data.
	Block        []*Task `json:"block,omitempty"`
	Rescue       []*Task `json:"rescue,omitempty"`
	Always       []*Task `json:"always,omitempty"`
	IncludeTasks string  `json:"includeTasks,omitempty"`
	ImportTasks  string  `json:"importTasks,omitempty"`
	Until        string  `json:"until,omitempty"`
	Retries      *int    `json:"retries,omitempty"`
	Delay        *int    `json:"delay,omitempty"`

	// Extras carries task keys from imported YAML that Visualible does not
	// model explicitly. They are preserved verbatim and re-emitted on
	// render, so import → edit → export never silently drops data.
	Extras map[string]any `json:"extras,omitempty"`
}

// Inventory is a named set of groups and hosts.
type Inventory struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Groups []*InventoryGroup `json:"groups"`
	Hosts  []*Host           `json:"hosts"` // ungrouped hosts
}

// InventoryGroup is an Ansible inventory group; Children enables nesting.
type InventoryGroup struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Hosts    []*Host           `json:"hosts"`
	Children []*InventoryGroup `json:"children,omitempty"`
	Vars     map[string]any    `json:"vars,omitempty"`
}

// Host is a single managed node.
type Host struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Address overrides Name for connection (ansible_host).
	Address string `json:"address,omitempty"`
	// SSHUser and SSHPort map to ansible_user / ansible_port.
	SSHUser string `json:"sshUser,omitempty"`
	SSHPort int    `json:"sshPort,omitempty"`
	// CredentialID references the credential store; secrets are never
	// inlined into inventory unless the user explicitly exports them.
	CredentialID string         `json:"credentialId,omitempty"`
	Vars         map[string]any `json:"vars,omitempty"`
}

// Variable is a named key/value used where order matters in the UI.
type Variable struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// CredentialReference points at a stored credential without exposing it.
type CredentialReference struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // ssh_key, ssh_password, become_password, api_key, ...
}

// DeploymentPlan is the validated, renderer-independent input to a
// deployment backend.
type DeploymentPlan struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"projectId"`
	PlaybookID  string   `json:"playbookId"`
	InventoryID string   `json:"inventoryId"`
	Tags        []string `json:"tags,omitempty"`
	Limit       string   `json:"limit,omitempty"`
	Check       bool     `json:"check,omitempty"`
	Diff        bool     `json:"diff,omitempty"`
	Verbosity   int      `json:"verbosity,omitempty"` // 0-4
}

// Deployment records one execution (or attempted execution) of a plan.
type Deployment struct {
	ID         string           `json:"id"`
	Plan       *DeploymentPlan  `json:"plan"`
	Status     DeploymentStatus `json:"status"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt *time.Time       `json:"finishedAt,omitempty"`
	ExitCode   *int             `json:"exitCode,omitempty"`
}

type DeploymentStatus string

const (
	DeploymentPending   DeploymentStatus = "pending"
	DeploymentRunning   DeploymentStatus = "running"
	DeploymentSucceeded DeploymentStatus = "succeeded"
	DeploymentFailed    DeploymentStatus = "failed"
	DeploymentCanceled  DeploymentStatus = "canceled"
)

// DeploymentEventType is a normalized execution event kind. Raw terminal
// output is never the only execution API; consumers use these events.
type DeploymentEventType string

const (
	EventDeploymentStarted  DeploymentEventType = "deployment.started"
	EventDeploymentFinished DeploymentEventType = "deployment.finished"
	EventPlayStarted        DeploymentEventType = "play.started"
	EventPlayFinished       DeploymentEventType = "play.finished"
	EventHostStarted        DeploymentEventType = "host.started"
	EventHostFinished       DeploymentEventType = "host.finished"
	EventHostUnreachable    DeploymentEventType = "host.unreachable"
	EventTaskStarted        DeploymentEventType = "task.started"
	EventTaskOK             DeploymentEventType = "task.ok"
	EventTaskChanged        DeploymentEventType = "task.changed"
	EventTaskSkipped        DeploymentEventType = "task.skipped"
	EventTaskFailed         DeploymentEventType = "task.failed"
	EventHandlerStarted     DeploymentEventType = "handler.started"
	EventLogStdout          DeploymentEventType = "log.stdout"
	EventLogStderr          DeploymentEventType = "log.stderr"
)

// DeploymentEvent is the normalized unit streamed to clients.
type DeploymentEvent struct {
	Type         DeploymentEventType `json:"type"`
	Timestamp    time.Time           `json:"timestamp"`
	DeploymentID string              `json:"deploymentId"`
	Play         string              `json:"play,omitempty"`
	Task         string              `json:"task,omitempty"`
	TaskID       string              `json:"taskId,omitempty"`
	Host         string              `json:"host,omitempty"`
	Status       string              `json:"status,omitempty"`
	Changed      bool                `json:"changed,omitempty"`
	Message      string              `json:"message,omitempty"`
	DurationMS   int64               `json:"durationMs,omitempty"`
}
