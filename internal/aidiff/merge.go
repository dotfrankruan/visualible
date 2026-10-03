package aidiff

import (
	"fmt"
	"sort"

	"github.com/dotfrankruan/visualible/internal/ir"
)

// PlayChangeID is the synthetic change ID for play-level field changes
// (name/hosts/become/vars).
const PlayChangeID = "play:modified"

// Merge applies a subset of diff changes to the base playbook, producing
// a new playbook. It never mutates its inputs.
//
// Content rules (per change):
//   - added: included only when accepted (proposed content)
//   - modified: proposed content when accepted, base content otherwise
//   - moved/unchanged: base content (identical by definition)
//   - removed: excluded only when accepted
//
// Position rules: accepted additions/modifications/moves follow the
// proposed order; rejected modifications/moves and kept removals retain
// their base positions. Ties resolve deterministically by base position.
//
// The merged playbook is validated before returning; dependency problems
// (e.g. accepting a notify whose handler addition was rejected) surface
// as validation errors and block the merge.
func Merge(base, proposed *ir.Playbook, d *Diff, accepted map[string]bool) (*ir.Playbook, error) {
	if base == nil || proposed == nil {
		return nil, fmt.Errorf("base and proposed playbooks are required")
	}
	out := &ir.Playbook{ID: base.ID, Name: base.Name}
	if len(base.Plays) == 0 {
		// No base play: the proposed play is one big addition; accepting
		// everything-or-nothing is the only coherent merge.
		if len(proposed.Plays) > 0 {
			out.Plays = append(out.Plays, cloneTasksOf(proposed.Plays[0]))
		}
		return validateMerged(out)
	}
	bp := base.Plays[0]
	pp := proposed.Plays[0]

	mergedPlay := &ir.Play{
		ID:     bp.ID,
		Name:   bp.Name,
		Hosts:  bp.Hosts,
		Become: bp.Become,
		Vars:   bp.Vars,
		Roles:  bp.Roles,
		Tags:   bp.Tags,
	}
	if accepted[PlayChangeID] && len(proposed.Plays) > 0 {
		mergedPlay.Name = pp.Name
		mergedPlay.Hosts = pp.Hosts
		mergedPlay.Become = pp.Become
		mergedPlay.Vars = pp.Vars
	}

	byID := map[string]*Change{}
	for i := range d.Changes {
		byID[d.Changes[i].ID] = &d.Changes[i]
	}

	for _, sec := range taskSections {
		baseList := sec.get(bp)
		proposedList := sec.get(pp)
		merged := mergeTaskList(sec.name, baseList, proposedList, byID, accepted)
		switch sec.name {
		case "pre_tasks":
			mergedPlay.PreTasks = merged
		case "tasks":
			mergedPlay.Tasks = merged
		case "post_tasks":
			mergedPlay.PostTasks = merged
		case "handlers":
			mergedPlay.Handlers = merged
		}
	}
	out.Plays = append(out.Plays, mergedPlay)
	// Additional plays (outside v1 editor scope) pass through unchanged.
	out.Plays = append(out.Plays, base.Plays[1:]...)
	return validateMerged(out)
}

func mergeTaskList(section string, base, proposed []*ir.Task, changes map[string]*Change, accepted map[string]bool) []*ir.Task {
	type entry struct {
		task *ir.Task
		pos  int // preferred position
		base int // base index (tiebreak)
	}
	entries := map[string]entry{}
	seenProposed := map[string]bool{}

	for pi, pt := range proposed {
		seenProposed[pt.ID] = true
		c := findChange(changes, section, pt.ID)
		switch {
		case c == nil:
			// Should not happen; treat as accepted addition.
			entries[pt.ID] = entry{task: pt, pos: pi, base: len(base) + pi}
		case c.Kind == Added:
			if accepted[c.ID] {
				entries[pt.ID] = entry{task: pt, pos: pi, base: len(base) + pi}
			}
		case c.Kind == Modified:
			if accepted[c.ID] {
				entries[pt.ID] = entry{task: pt, pos: pi, base: c.FromPos}
			} else {
				entries[pt.ID] = entry{task: base[c.FromPos], pos: c.FromPos, base: c.FromPos}
			}
		default: // Moved, Unchanged: content identical
			pos := c.FromPos
			if c.Kind == Moved && accepted[c.ID] {
				pos = pi
			}
			entries[pt.ID] = entry{task: base[c.FromPos], pos: pos, base: c.FromPos}
		}
	}

	// Removals: base tasks absent from proposed.
	for bi, bt := range base {
		if seenProposed[bt.ID] {
			continue
		}
		c := findChange(changes, section, bt.ID)
		if c != nil && c.Kind == Removed && accepted[c.ID] {
			continue // removal accepted
		}
		entries[bt.ID] = entry{task: bt, pos: bi, base: bi}
	}

	ordered := make([]entry, 0, len(entries))
	for _, e := range entries {
		ordered = append(ordered, e)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].pos != ordered[j].pos {
			return ordered[i].pos < ordered[j].pos
		}
		return ordered[i].base < ordered[j].base
	})
	var out []*ir.Task
	for _, e := range ordered {
		out = append(out, e.task)
	}
	if out == nil {
		out = []*ir.Task{}
	}
	return out
}

func findChange(changes map[string]*Change, section, taskID string) *Change {
	for _, kind := range []ChangeKind{Added, Removed, Modified, Moved, Unchanged} {
		if c := changes[changeID(section, kind, taskID)]; c != nil {
			return c
		}
	}
	return nil
}

func cloneTasksOf(p *ir.Play) *ir.Play {
	cp := *p
	return &cp
}

func validateMerged(pb *ir.Playbook) (*ir.Playbook, error) {
	if err := pb.Validate(); err != nil {
		if ve, ok := err.(*ir.ValidationError); ok {
			return nil, fmt.Errorf("merged playbook would be invalid: %v", ve.Problems)
		}
		return nil, fmt.Errorf("merged playbook would be invalid: %w", err)
	}
	return pb, nil
}
