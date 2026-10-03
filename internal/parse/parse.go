// Package parse converts Ansible playbook YAML into Visualible IR. It
// supports the documented v0.1 subset, reports structured diagnostics for
// everything else, and preserves unmodeled keys via Task.Extras so import
// never silently drops data. Imported YAML is treated as untrusted input:
// parsing only builds data structures — it never executes anything.
package parse

import (
	"crypto/rand"
	"fmt"
	"sort"

	"github.com/visualible/visualible/internal/ir"
	"gopkg.in/yaml.v3"
)

// Severity levels for diagnostics.
const (
	Warning = "warning"
	Error   = "error"
)

// Diagnostic describes one problem or caveat found while parsing.
type Diagnostic struct {
	Severity string `json:"severity"` // warning | error
	Path     string `json:"path"`     // e.g. plays[0].tasks[2]
	Message  string `json:"message"`
}

// Result is the outcome of parsing: the IR (possibly partially populated)
// plus every diagnostic encountered.
type Result struct {
	Playbook    *ir.Playbook `json:"playbook"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (r *Result) warn(path, format string, args ...any) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity: Warning, Path: path, Message: fmt.Sprintf(format, args...),
	})
}

func (r *Result) errorf(path, format string, args ...any) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity: Error, Path: path, Message: fmt.Sprintf(format, args...),
	})
}

// HasErrors reports whether any error-severity diagnostic was recorded.
func (r *Result) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%x", prefix, b)
}

// Playbook parses Ansible playbook YAML (a list of plays) into IR.
// A non-empty Result.Playbook is always returned on success; check
// Diagnostics for unsupported constructs.
func Playbook(name string, data []byte) (*Result, error) {
	var raw []any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("empty document: a playbook must be a list of plays")
	}
	res := &Result{
		Playbook: &ir.Playbook{ID: newID("pb"), Name: name},
	}
	for i, item := range raw {
		path := fmt.Sprintf("plays[%d]", i)
		m, ok := asMap(item)
		if !ok {
			res.errorf(path, "play must be a mapping, got %s", kindOf(item))
			continue
		}
		res.Playbook.Plays = append(res.Playbook.Plays, res.parsePlay(path, m))
	}
	return res, nil
}

// commonTaskKeys are consumed during parsing; any other key is either the
// module invocation or an unmodeled extra.
var commonTaskKeys = map[string]bool{
	"name": true, "when": true, "loop": true, "register": true,
	"notify": true, "tags": true, "become": true, "delegate_to": true,
	"run_once": true, "environment": true, "changed_when": true,
	"failed_when": true, "until": true, "retries": true, "delay": true,
	"include_tasks": true, "import_tasks": true,
	"block": true, "rescue": true, "always": true,
	"vars": true, // task-level vars: preserved via extras
}

func (r *Result) parsePlay(path string, m map[string]any) *ir.Play {
	play := &ir.Play{ID: newID("play")}
	for k, v := range m {
		switch k {
		case "name":
			play.Name = asString(v)
		case "hosts":
			play.Hosts = asString(v)
		case "become":
			play.Become = asBool(v)
		case "vars":
			if vm, ok := asMap(v); ok {
				play.Vars = vm
			} else {
				r.warn(path, "vars must be a mapping; ignored")
			}
		case "tags":
			play.Tags = asStringList(v)
		case "roles":
			play.Roles = r.parseRoles(path, v)
		case "pre_tasks":
			play.PreTasks = r.parseTaskList(path+".pre_tasks", v)
		case "tasks":
			play.Tasks = r.parseTaskList(path+".tasks", v)
		case "post_tasks":
			play.PostTasks = r.parseTaskList(path+".post_tasks", v)
		case "handlers":
			play.Handlers = r.parseTaskList(path+".handlers", v)
		default:
			r.warn(path, "unsupported play key %q: dropped (not modeled)", k)
		}
	}
	if play.Name == "" {
		play.Name = play.Hosts
	}
	return play
}

func (r *Result) parseRoles(path string, v any) []ir.RoleReference {
	list, ok := v.([]any)
	if !ok {
		r.warn(path, "roles must be a list; ignored")
		return nil
	}
	var out []ir.RoleReference
	for i, item := range list {
		rp := fmt.Sprintf("%s.roles[%d]", path, i)
		switch role := item.(type) {
		case string:
			out = append(out, ir.RoleReference{Role: role})
		case map[string]any:
			ref := ir.RoleReference{}
			if rn, ok := role["role"].(string); ok {
				ref.Role = rn
			} else if rn, ok := role["name"].(string); ok {
				ref.Role = rn
			} else {
				r.warn(rp, "role entry has no role/name; skipped")
				continue
			}
			if vm, ok := asMap(role["vars"]); ok {
				ref.Vars = vm
			}
			ref.Tags = asStringList(role["tags"])
			out = append(out, ref)
		default:
			r.warn(rp, "role entry must be a string or mapping; skipped")
		}
	}
	return out
}

func (r *Result) parseTaskList(path string, v any) []*ir.Task {
	list, ok := v.([]any)
	if !ok {
		r.warn(path, "task section must be a list; ignored")
		return nil
	}
	var out []*ir.Task
	for i, item := range list {
		tp := fmt.Sprintf("%s[%d]", path, i)
		m, ok := asMap(item)
		if !ok {
			r.errorf(tp, "task must be a mapping, got %s; skipped", kindOf(item))
			continue
		}
		out = append(out, r.parseTask(tp, m))
	}
	return out
}

func (r *Result) parseTask(path string, m map[string]any) *ir.Task {
	t := &ir.Task{ID: newID("task")}

	// Separate module candidates from common keys and extras. Legacy
	// action/local_action forms are never treated as modules.
	var moduleKeys []string
	legacyKeys := []string{}
	for k := range m {
		switch {
		case k == "action" || k == "local_action":
			legacyKeys = append(legacyKeys, k)
		case !commonTaskKeys[k]:
			moduleKeys = append(moduleKeys, k)
		}
	}
	for _, k := range legacyKeys {
		r.warn(path, "legacy %q form is not supported; preserved as extras", k)
	}
	if len(legacyKeys) > 0 {
		if t.Extras == nil {
			t.Extras = map[string]any{}
		}
		for _, k := range legacyKeys {
			t.Extras[k] = m[k]
		}
	}
	switch len(moduleKeys) {
	case 0:
		// Structural-only task (block/include) or invalid; validation
		// will flag it if no structural field is set below.
	case 1:
		t.Module = moduleKeys[0]
		if args, ok := asMap(m[moduleKeys[0]]); ok {
			t.Args = args
		} else if s, isStr := m[moduleKeys[0]].(string); isStr && s == "" {
			t.Args = map[string]any{}
		} else {
			r.warn(path, "module %q arguments should be a mapping; preserved raw", moduleKeys[0])
			t.Args = map[string]any{"_raw_params": m[moduleKeys[0]]}
		}
	default:
		sort.Strings(moduleKeys)
		r.errorf(path, "task has multiple candidate module keys %v; preserved as extras (needs manual review)", moduleKeys)
		t.Extras = map[string]any{}
		for _, k := range moduleKeys {
			t.Extras[k] = m[k]
		}
	}

	for k, v := range m {
		switch k {
		case "name":
			t.Name = asString(v)
		case "when":
			t.When = asString(v)
		case "loop":
			t.Loop = v
		case "register":
			t.Register = asString(v)
		case "notify":
			t.Notify = asStringList(v)
		case "tags":
			t.Tags = asStringList(v)
		case "become":
			if b, ok := v.(bool); ok {
				t.Become = &b
			} else {
				r.warn(path, "become must be a boolean; ignored")
			}
		case "delegate_to":
			t.DelegateTo = asString(v)
		case "run_once":
			t.RunOnce = asBool(v)
		case "environment":
			if em, ok := asMap(v); ok {
				t.Environment = map[string]string{}
				for ek, ev := range em {
					t.Environment[ek] = asString(ev)
				}
			} else {
				r.warn(path, "environment must be a mapping; ignored")
			}
		case "changed_when":
			t.ChangedWhen = asString(v)
		case "failed_when":
			t.FailedWhen = asString(v)
		case "until":
			t.Until = asString(v)
		case "retries":
			if n, ok := asInt(v); ok {
				t.Retries = &n
			} else {
				r.warn(path, "retries must be an integer; ignored")
			}
		case "delay":
			if n, ok := asInt(v); ok {
				t.Delay = &n
			} else {
				r.warn(path, "delay must be an integer; ignored")
			}
		case "include_tasks":
			t.IncludeTasks = asString(v)
		case "import_tasks":
			t.ImportTasks = asString(v)
		case "block":
			t.Block = r.parseTaskList(path+".block", v)
			r.warn(path, "block/rescue/always is parsed but not editable in the v0.1 UI")
		case "rescue":
			t.Rescue = r.parseTaskList(path+".rescue", v)
		case "always":
			t.Always = r.parseTaskList(path+".always", v)
		case "vars":
			// Task-level vars are unmodeled; preserve.
			if t.Extras == nil {
				t.Extras = map[string]any{}
			}
			t.Extras["vars"] = v
		}
	}
	if t.Name == "" && t.Module != "" {
		t.Name = t.Module
	}
	return t
}

// --- typed extraction helpers (YAML data is untrusted) ---

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", s)
	}
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func asStringList(v any) []string {
	switch l := v.(type) {
	case string:
		return []string{l}
	case []any:
		out := make([]string, 0, len(l))
		for _, item := range l {
			out = append(out, asString(item))
		}
		return out
	}
	return nil
}

func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "mapping"
	case []any:
		return "list"
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		return "scalar"
	}
}
