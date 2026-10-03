// Package render converts Visualible IR into Ansible YAML. Rendering uses
// explicitly ordered YAML nodes so output is canonical and human-readable;
// it never attempts to preserve the formatting of imported files (the
// import direction lives in the parse package).
package render

import (
	"fmt"
	"sort"

	"github.com/dotfrankruan/visualible/internal/ir"
	"gopkg.in/yaml.v3"
)

// Field order in yamlPlay defines the canonical play key order.
type yamlPlay struct {
	Name      string         `yaml:"name"`
	Hosts     string         `yaml:"hosts"`
	Become    bool           `yaml:"become,omitempty"`
	Vars      map[string]any `yaml:"vars,omitempty"`
	Roles     []any          `yaml:"roles,omitempty"`
	Tags      []string       `yaml:"tags,omitempty"`
	PreTasks  []*yamlTask    `yaml:"pre_tasks,omitempty"`
	Tasks     []*yamlTask    `yaml:"tasks,omitempty"`
	PostTasks []*yamlTask    `yaml:"post_tasks,omitempty"`
	Handlers  []*yamlTask    `yaml:"handlers,omitempty"`
}

// yamlTask renders a task with explicit key ordering: name first, then the
// module invocation, then common keys in their canonical order — matching
// idiomatic Ansible style:
//
//   - name: Install nginx
//     ansible.builtin.apt:
//     name: nginx
//     state: present
//     notify:
//   - Restart nginx
type yamlTask struct {
	name   string
	module string
	args   map[string]any
	// fields holds common task keys in insertion order.
	fields []taskField
}

type taskField struct {
	key   string
	value any
}

// MarshalYAML emits the task as an ordered mapping node.
func (t *yamlTask) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	put := func(k string, v any) error {
		vn := &yaml.Node{}
		if err := vn.Encode(v); err != nil {
			return err
		}
		n.Content = append(n.Content, strNode(k), vn)
		return nil
	}
	if err := put("name", t.name); err != nil {
		return nil, err
	}
	if t.module != "" {
		args := t.args
		if args == nil {
			args = map[string]any{}
		}
		if err := put(t.module, args); err != nil {
			return nil, err
		}
	}
	for _, f := range t.fields {
		if err := put(f.key, f.value); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// UnsupportedError reports IR features that exist in the model but cannot
// yet be rendered. Rendering refuses to silently reinterpret them.
type UnsupportedError struct {
	Features []string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("unsupported features for rendering: %v", e.Features)
}

// Playbook renders an IR playbook as an Ansible playbook YAML document
// (a YAML list of plays). The playbook must pass IR validation.
func Playbook(pb *ir.Playbook) ([]byte, error) {
	if err := pb.Validate(); err != nil {
		return nil, fmt.Errorf("cannot render invalid playbook: %w", err)
	}
	if unsupported := unsupportedFeatures(pb); len(unsupported) > 0 {
		return nil, &UnsupportedError{Features: unsupported}
	}
	plays := make([]yamlPlay, 0, len(pb.Plays))
	for _, p := range pb.Plays {
		plays = append(plays, renderPlay(p))
	}
	out, err := yaml.Marshal(plays)
	if err != nil {
		return nil, fmt.Errorf("marshal playbook: %w", err)
	}
	return out, nil
}

func unsupportedFeatures(pb *ir.Playbook) []string {
	var out []string
	walk := func(section string, tasks []*ir.Task) {
		for _, t := range tasks {
			if len(t.Block) > 0 || len(t.Rescue) > 0 || len(t.Always) > 0 {
				out = append(out, fmt.Sprintf("%s task %q: block/rescue/always", section, t.Name))
			}
		}
	}
	for _, p := range pb.Plays {
		walk("tasks", p.Tasks)
		walk("pre_tasks", p.PreTasks)
		walk("post_tasks", p.PostTasks)
		walk("handlers", p.Handlers)
	}
	return out
}

func renderPlay(p *ir.Play) yamlPlay {
	yp := yamlPlay{
		Name:   p.Name,
		Hosts:  p.Hosts,
		Become: p.Become,
		Vars:   p.Vars,
		Tags:   p.Tags,
	}
	for _, r := range p.Roles {
		if len(r.Vars) == 0 && len(r.Tags) == 0 {
			yp.Roles = append(yp.Roles, r.Role)
		} else {
			m := map[string]any{"role": r.Role}
			if len(r.Vars) > 0 {
				m["vars"] = r.Vars
			}
			if len(r.Tags) > 0 {
				m["tags"] = r.Tags
			}
			yp.Roles = append(yp.Roles, m)
		}
	}
	yp.PreTasks = renderTasks(p.PreTasks)
	yp.Tasks = renderTasks(p.Tasks)
	yp.PostTasks = renderTasks(p.PostTasks)
	yp.Handlers = renderTasks(p.Handlers)
	return yp
}

func renderTasks(tasks []*ir.Task) []*yamlTask {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]*yamlTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, renderTask(t))
	}
	return out
}

func renderTask(t *ir.Task) *yamlTask {
	yt := &yamlTask{name: t.Name, module: t.Module, args: t.Args}
	add := func(cond bool, key string, value any) {
		if cond {
			yt.fields = append(yt.fields, taskField{key: key, value: value})
		}
	}
	add(t.When != "", "when", t.When)
	add(t.Loop != nil, "loop", t.Loop)
	add(t.Register != "", "register", t.Register)
	add(len(t.Notify) > 0, "notify", t.Notify)
	add(len(t.Tags) > 0, "tags", t.Tags)
	add(t.Become != nil, "become", t.Become != nil && *t.Become)
	add(t.DelegateTo != "", "delegate_to", t.DelegateTo)
	add(t.RunOnce, "run_once", true)
	add(len(t.Environment) > 0, "environment", t.Environment)
	add(t.ChangedWhen != "", "changed_when", t.ChangedWhen)
	add(t.FailedWhen != "", "failed_when", t.FailedWhen)
	add(t.Until != "", "until", t.Until)
	if t.Retries != nil {
		yt.fields = append(yt.fields, taskField{key: "retries", value: *t.Retries})
	}
	if t.Delay != nil {
		yt.fields = append(yt.fields, taskField{key: "delay", value: *t.Delay})
	}
	add(t.IncludeTasks != "", "include_tasks", t.IncludeTasks)
	add(t.ImportTasks != "", "import_tasks", t.ImportTasks)
	// Imported keys we do not model are re-emitted verbatim (sorted for
	// canonical output) so import → edit → export never loses data.
	if len(t.Extras) > 0 {
		keys := make([]string, 0, len(t.Extras))
		for k := range t.Extras {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			yt.fields = append(yt.fields, taskField{key: k, value: t.Extras[k]})
		}
	}
	return yt
}
