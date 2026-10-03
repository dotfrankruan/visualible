// Package action implements Visualible's curated "Actions": beginner
// friendly automation building blocks that produce ordinary IR tasks.
//
// Actions are a presentation layer only. They never replace the IR: an
// Action generates tasks, and recognition maps existing tasks back to
// Actions when — and only when — the task can be represented faithfully.
// Anything else stays an Advanced Ansible task, so no configuration is
// ever hidden or lost.
package action

import (
	"fmt"

	"github.com/dotfrankruan/visualible/internal/ir"
)

// Choice is one option of a select field. Value is what the IR receives;
// Label is what the user reads.
type Choice struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

// Field describes one curated form field.
type Field struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text | textarea | select | bool
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
	Choices     []Choice `json:"choices,omitempty"`
}

// Definition is a curated Action as exposed to the UI.
type Definition struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Icon        string  `json:"icon"`
	Summary     string  `json:"summary"`
	Explanation string  `json:"explanation"`
	Fields      []Field `json:"fields"`

	// RequiresCollection gates the Action on an installed Ansible
	// collection (e.g. community.docker). Empty means always available.
	RequiresCollection string `json:"requiresCollection,omitempty"`

	// Availability fields are filled per request by the server.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Generated is the IR produced by an Action. Simple Actions may generate
// several tasks (and handlers) when that matches the user's intent better
// than exposing the underlying modules.
type Generated struct {
	Tasks    []*ir.Task `json:"tasks"`
	Handlers []*ir.Task `json:"handlers,omitempty"`
}

// Recognition maps one existing task back to an Action.
type Recognition struct {
	TaskID     string         `json:"taskId"`
	Recognized bool           `json:"recognized"`
	Action     string         `json:"action,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	Icon       string         `json:"icon,omitempty"`
	Label      string         `json:"label,omitempty"`    // "Install nginx"
	Subtitle   string         `json:"subtitle,omitempty"` // "Package: nginx · Installed"
}

// implementation is one Action: its definition plus generation and
// recognition behaviour.
type implementation interface {
	Definition() Definition
	Generate(params map[string]any) (*Generated, error)
	// Recognize returns the curated params when the task maps faithfully
	// to this Action, and false otherwise.
	Recognize(t *ir.Task) (map[string]any, bool)
	// Describe renders user-facing copy for recognized params.
	Describe(params map[string]any) (label, subtitle string)
}

var registry = map[string]implementation{}

// order controls presentation order in the UI.
var order = []string{}

func register(impl implementation) {
	def := impl.Definition()
	registry[def.ID] = impl
	order = append(order, def.ID)
}

func init() {
	register(installSoftware{})
	register(manageService{})
	register(createUser{})
	register(addSSHKey{})
	register(deployFile{})
	register(renderTemplate{})
	register(createDirectory{})
	register(runCommand{})
	register(dockerContainer{})
}

// Definitions returns all Action definitions in presentation order.
func Definitions() []Definition {
	out := make([]Definition, 0, len(order))
	for _, id := range order {
		def := registry[id].Definition()
		def.Available = true
		out = append(out, def)
	}
	return out
}

// Generate runs an Action with curated params.
func Generate(actionID string, params map[string]any) (*Generated, error) {
	impl, ok := registry[actionID]
	if !ok {
		return nil, fmt.Errorf("Unknown automation action %q.", actionID)
	}
	gen, err := impl.Generate(params)
	if err != nil {
		return nil, err
	}
	if gen == nil || (len(gen.Tasks) == 0 && len(gen.Handlers) == 0) {
		return nil, fmt.Errorf("this automation action produced nothing to do")
	}
	if gen.Tasks == nil {
		gen.Tasks = []*ir.Task{}
	}
	return gen, nil
}

// RecognizeOne attempts to map a single task to a curated Action. It is
// deliberately strict: unusual module arguments or advanced task options
// mean the task cannot be represented in Simple mode, and the caller
// should show it as an Advanced Ansible task instead.
func RecognizeOne(t *ir.Task) Recognition {
	// Try registered actions in presentation order for deterministic
	// results when several recognizers could match.
	for _, id := range order {
		params, ok := registry[id].Recognize(t)
		if !ok {
			continue
		}
		label, subtitle := registry[id].Describe(params)
		return Recognition{
			TaskID:     t.ID,
			Recognized: true,
			Action:     id,
			Params:     params,
			Icon:       registry[id].Definition().Icon,
			Label:      label,
			Subtitle:   subtitle,
		}
	}
	return Recognition{TaskID: t.ID, Recognized: false}
}

// RecognizeAll maps a batch of tasks (and handlers).
func RecognizeAll(tasks []*ir.Task) []Recognition {
	out := make([]Recognition, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, RecognizeOne(t))
	}
	return out
}

// --- shared helpers -------------------------------------------------

func newID(prefix string) string {
	var b [4]byte
	mustRand(b[:])
	return fmt.Sprintf("%s-%x", prefix, b)
}

// uncatalogued reports whether a task carries task-level options outside
// the curated Action's reach. Such tasks must stay Advanced.
func uncatalogued(t *ir.Task) bool {
	return t.When != "" || t.Loop != nil || t.Register != "" || len(t.Tags) > 0 ||
		t.DelegateTo != "" || t.RunOnce || len(t.Environment) > 0 ||
		t.ChangedWhen != "" || t.FailedWhen != "" || t.Until != "" ||
		t.Retries != nil || t.Delay != nil || t.IncludeTasks != "" ||
		t.ImportTasks != "" || len(t.Block) > 0 || len(t.Rescue) > 0 ||
		len(t.Always) > 0 || len(t.Extras) > 0
}

// match checks module, allowed argument keys and task-level fidelity.
func match(t *ir.Task, modules, allowedArgs []string, allowNotify bool) (map[string]any, bool) {
	if uncatalogued(t) {
		return nil, false
	}
	if len(t.Notify) > 0 && !allowNotify {
		return nil, false
	}
	if !contains(modules, t.Module) {
		return nil, false
	}
	for k := range t.Args {
		if !contains(allowedArgs, k) {
			return nil, false
		}
	}
	if t.Args == nil {
		return map[string]any{}, true
	}
	return t.Args, true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func str(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

func boolParam(params map[string]any, key string) bool {
	v, _ := params[key].(bool)
	return v
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func argStringList(args map[string]any, key string) []string {
	switch v := args[key].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			} else {
				return nil
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

// stateLabel converts Ansible state values into plain language.
func stateLabel(module, state string) string {
	switch state {
	case "present", "latest", "installed":
		if state == "latest" {
			return "Up to date (latest)"
		}
		return "Installed"
	case "absent", "removed":
		return "Removed"
	case "started", "running":
		return "Running"
	case "stopped":
		return "Stopped"
	case "restarted":
		return "Restarted"
	case "reloaded":
		return "Reloaded"
	case "directory":
		return "Folder exists"
	case "file":
		return "File exists"
	case "touch":
		return "File touched"
	}
	return state
}
