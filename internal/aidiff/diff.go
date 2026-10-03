// Package aidiff implements Visualible's IR-aware semantic diff and
// merge. The AI model proposes a complete desired IR; Visualible — never
// the model — determines what changed, using stable object IDs. Rendered
// YAML is never diffed as text.
package aidiff

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/dotfrankruan/visualible/internal/ir"
)

// ChangeKind classifies one detected change.
type ChangeKind string

const (
	Added     ChangeKind = "added"
	Removed   ChangeKind = "removed"
	Modified  ChangeKind = "modified"
	Moved     ChangeKind = "moved" // position changed, content identical
	Unchanged ChangeKind = "unchanged"
)

// FieldChange describes one field-level difference within a modified
// task (or at play level).
type FieldChange struct {
	Path string `json:"path"` // e.g. "name", "become", "args.dest", "args.networks.name"
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

// Change is one item of the semantic diff. ID is stable across the
// proposal's lifetime and used for selective accept/reject.
type Change struct {
	ID       string        `json:"id"` // "tasks:modified:t-abc"
	Kind     ChangeKind    `json:"kind"`
	Section  string        `json:"section"` // tasks | handlers | pre_tasks | post_tasks
	TaskID   string        `json:"taskId"`
	Name     string        `json:"name"` // best available human name
	FromPos  int           `json:"fromPos,omitempty"`
	ToPos    int           `json:"toPos,omitempty"`
	Fields   []FieldChange `json:"fields,omitempty"`
	Base     *ir.Task      `json:"base,omitempty"`
	Proposed *ir.Task      `json:"proposed,omitempty"`
}

// Summary counts changes by kind (unchanged included for context).
type Summary struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Modified  int `json:"modified"`
	Moved     int `json:"moved"`
	Unchanged int `json:"unchanged"`
}

// Diff is the full semantic comparison of two playbooks.
type Diff struct {
	Changes     []Change      `json:"changes"`
	PlayChanges []FieldChange `json:"playChanges,omitempty"` // play-level (name/hosts/become/vars)
	Summary     Summary       `json:"summary"`
}

// taskSections names the diffed task lists of a play, in order.
var taskSections = []struct {
	name string
	get  func(*ir.Play) []*ir.Task
}{
	{"pre_tasks", func(p *ir.Play) []*ir.Task { return p.PreTasks }},
	{"tasks", func(p *ir.Play) []*ir.Task { return p.Tasks }},
	{"post_tasks", func(p *ir.Play) []*ir.Task { return p.PostTasks }},
	{"handlers", func(p *ir.Play) []*ir.Task { return p.Handlers }},
}

// Playbooks computes the semantic diff between a base and a proposed
// playbook. v1 scope: the first play of each (the editor's working
// scope); plays beyond the first pass through merge unchanged.
func Playbooks(base, proposed *ir.Playbook) *Diff {
	d := &Diff{}
	if base == nil || proposed == nil || len(proposed.Plays) == 0 {
		return d
	}
	// Empty base (new/empty project): everything is an addition.
	bp := &ir.Play{}
	if len(base.Plays) > 0 {
		bp = base.Plays[0]
	}
	pp := proposed.Plays[0]
	d.PlayChanges = diffPlayFields(bp, pp)
	for _, sec := range taskSections {
		d.Changes = append(d.Changes, diffTaskList(sec.name, sec.get(bp), sec.get(pp))...)
	}
	for _, c := range d.Changes {
		switch c.Kind {
		case Added:
			d.Summary.Added++
		case Removed:
			d.Summary.Removed++
		case Modified:
			d.Summary.Modified++
		case Moved:
			d.Summary.Moved++
		case Unchanged:
			d.Summary.Unchanged++
		}
	}
	return d
}

func diffPlayFields(base, proposed *ir.Play) []FieldChange {
	var out []FieldChange
	if base.Name != proposed.Name {
		out = append(out, FieldChange{Path: "play.name", From: base.Name, To: proposed.Name})
	}
	if base.Hosts != proposed.Hosts {
		out = append(out, FieldChange{Path: "play.hosts", From: base.Hosts, To: proposed.Hosts})
	}
	if base.Become != proposed.Become {
		out = append(out, FieldChange{Path: "play.become", From: base.Become, To: proposed.Become})
	}
	if !reflect.DeepEqual(base.Vars, proposed.Vars) {
		out = append(out, FieldChange{Path: "play.vars", From: base.Vars, To: proposed.Vars})
	}
	return out
}

func diffTaskList(section string, base, proposed []*ir.Task) []Change {
	baseByID := map[string]int{} // task ID -> index in base
	for i, t := range base {
		baseByID[t.ID] = i
	}
	proposedByID := map[string]int{}
	for i, t := range proposed {
		proposedByID[t.ID] = i
	}

	var changes []Change

	// Walk proposed: additions, modifications, moves, unchanged.
	for pi, pt := range proposed {
		bi, ok := baseByID[pt.ID]
		if !ok {
			changes = append(changes, Change{
				ID:       changeID(section, Added, pt.ID),
				Kind:     Added,
				Section:  section,
				TaskID:   pt.ID,
				Name:     taskName(pt),
				ToPos:    pi,
				Proposed: pt,
			})
			continue
		}
		bt := base[bi]
		fields := diffTaskFields(bt, pt)
		c := Change{
			Section:  section,
			TaskID:   pt.ID,
			Name:     taskName(pt),
			FromPos:  bi,
			ToPos:    pi,
			Base:     bt,
			Proposed: pt,
		}
		switch {
		case len(fields) > 0:
			c.ID = changeID(section, Modified, pt.ID)
			c.Kind = Modified
			c.Fields = fields
		case bi != pi:
			c.ID = changeID(section, Moved, pt.ID)
			c.Kind = Moved
		default:
			c.ID = changeID(section, Unchanged, pt.ID)
			c.Kind = Unchanged
			c.Base = nil // unchanged items need no payload
			c.Proposed = nil
		}
		changes = append(changes, c)
	}

	// Removals: in base, absent from proposed.
	for bi, bt := range base {
		if _, ok := proposedByID[bt.ID]; !ok {
			changes = append(changes, Change{
				ID:      changeID(section, Removed, bt.ID),
				Kind:    Removed,
				Section: section,
				TaskID:  bt.ID,
				Name:    taskName(bt),
				FromPos: bi,
				Base:    bt,
			})
		}
	}

	// Stable presentation order: by proposed position, removals interleaved
	// near their original spot, additions at their proposed spot.
	sort.SliceStable(changes, func(i, j int) bool {
		pi, pj := changes[i].sortPos(), changes[j].sortPos()
		if pi != pj {
			return pi < pj
		}
		return changes[i].ID < changes[j].ID
	})
	return changes
}

func (c Change) sortPos() int {
	if c.Kind == Added || c.Kind == Modified || c.Kind == Moved || c.Kind == Unchanged {
		return c.ToPos
	}
	return c.FromPos
}

func changeID(section string, kind ChangeKind, taskID string) string {
	return fmt.Sprintf("%s:%s:%s", section, kind, taskID)
}

func taskName(t *ir.Task) string {
	if t.Name != "" {
		return t.Name
	}
	return t.Module
}

// diffTaskFields computes field-level differences between two versions of
// the same task. nil means identical.
func diffTaskFields(a, b *ir.Task) []FieldChange {
	var out []FieldChange
	cmp := func(path string, from, to any) {
		if !reflect.DeepEqual(from, to) {
			out = append(out, FieldChange{Path: path, From: from, To: to})
		}
	}
	cmp("name", a.Name, b.Name)
	cmp("module", a.Module, b.Module)
	cmp("when", a.When, b.When)
	cmp("register", a.Register, b.Register)
	cmp("delegate_to", a.DelegateTo, b.DelegateTo)
	cmp("changed_when", a.ChangedWhen, b.ChangedWhen)
	cmp("failed_when", a.FailedWhen, b.FailedWhen)
	cmp("become", boolVal(a.Become), boolVal(b.Become))
	cmp("run_once", a.RunOnce, b.RunOnce)
	cmp("until", a.Until, b.Until)
	cmp("retries", intVal(a.Retries), intVal(b.Retries))
	cmp("delay", intVal(a.Delay), intVal(b.Delay))
	cmp("notify", a.Notify, b.Notify)
	cmp("tags", a.Tags, b.Tags)
	cmp("loop", a.Loop, b.Loop)
	cmp("environment", a.Environment, b.Environment)
	cmp("include_tasks", a.IncludeTasks, b.IncludeTasks)
	cmp("import_tasks", a.ImportTasks, b.ImportTasks)
	diffArgs("args", a.Args, b.Args, &out)
	return out
}

func boolVal(p *bool) bool { return p != nil && *p }

func intVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// diffArgs recursively compares module argument maps.
func diffArgs(prefix string, a, b map[string]any, out *[]FieldChange) {
	if len(a) == 0 && len(b) == 0 {
		return
	}
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		av, aok := a[k]
		bv, bok := b[k]
		path := prefix + "." + k
		switch {
		case !aok:
			*out = append(*out, FieldChange{Path: path, To: bv})
		case !bok:
			*out = append(*out, FieldChange{Path: path, From: av})
		default:
			am, aIsMap := av.(map[string]any)
			bm, bIsMap := bv.(map[string]any)
			if aIsMap && bIsMap {
				diffArgs(path, am, bm, out)
				continue
			}
			if !reflect.DeepEqual(av, bv) {
				*out = append(*out, FieldChange{Path: path, From: av, To: bv})
			}
		}
	}
}

// HashIR returns a stable content hash of a playbook for stale-proposal
// detection. Map keys are sorted by encoding/json; struct field order is
// fixed, so equal IR always hashes equally.
func HashIR(pb *ir.Playbook) (string, error) {
	data, err := json.Marshal(pb)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}
