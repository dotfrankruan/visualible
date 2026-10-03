package ir

import (
	"fmt"
	"strings"
)

// ValidationError collects all problems found while validating IR so the
// caller can report them together instead of failing on the first one.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid IR: " + strings.Join(e.Problems, "; ")
}

func (e *ValidationError) add(format string, args ...any) {
	e.Problems = append(e.Problems, fmt.Sprintf(format, args...))
}

// Validate checks the project for structural correctness.
func (p *Project) Validate() error {
	v := &ValidationError{}
	if p.ID == "" {
		v.add("project: id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		v.add("project: name is required")
	}
	seen := map[string]bool{}
	for _, pb := range p.Playbooks {
		if seen[pb.ID] {
			v.add("project %q: duplicate playbook id %q", p.Name, pb.ID)
		}
		seen[pb.ID] = true
		if err := pb.Validate(); err != nil {
			if ve, ok := err.(*ValidationError); ok {
				v.Problems = append(v.Problems, ve.Problems...)
				continue
			}
			v.add("%s", err)
		}
	}
	for _, inv := range p.Inventories {
		if seen[inv.ID] {
			v.add("project %q: duplicate inventory id %q", p.Name, inv.ID)
		}
		seen[inv.ID] = true
		if err := inv.Validate(); err != nil {
			if ve, ok := err.(*ValidationError); ok {
				v.Problems = append(v.Problems, ve.Problems...)
				continue
			}
			v.add("%s", err)
		}
	}
	if len(v.Problems) > 0 {
		return v
	}
	return nil
}

// Validate checks the playbook and all contained plays/tasks.
func (pb *Playbook) Validate() error {
	v := &ValidationError{}
	if pb.ID == "" {
		v.add("playbook %q: id is required", pb.Name)
	}
	if strings.TrimSpace(pb.Name) == "" {
		v.add("playbook: name is required")
	}
	if len(pb.Plays) == 0 {
		v.add("playbook %q: at least one play is required", pb.Name)
	}
	for _, play := range pb.Plays {
		play.validateInto(v, pb.Name)
	}
	if len(v.Problems) > 0 {
		return v
	}
	return nil
}

func (p *Play) validateInto(v *ValidationError, pbName string) {
	ctx := fmt.Sprintf("playbook %q play %q", pbName, p.Name)
	if p.ID == "" {
		v.add("%s: id is required", ctx)
	}
	if strings.TrimSpace(p.Hosts) == "" {
		v.add("%s: hosts is required", ctx)
	}
	validateTaskList(v, ctx, "tasks", p.Tasks, p.Handlers)
	validateTaskList(v, ctx, "pre_tasks", p.PreTasks, p.Handlers)
	validateTaskList(v, ctx, "post_tasks", p.PostTasks, p.Handlers)
	validateTaskList(v, ctx, "handlers", p.Handlers, p.Handlers)
}

// validateTaskList checks one list of tasks; handlerNames is the set of
// handler names available for notify references (handlers of the play).
func validateTaskList(v *ValidationError, ctx, section string, tasks []*Task, handlers []*Task) {
	handlerNames := map[string]bool{}
	for _, h := range handlers {
		handlerNames[h.Name] = true
	}
	seen := map[string]bool{}
	for i, t := range tasks {
		tctx := fmt.Sprintf("%s %s[%d] %q", ctx, section, i, t.Name)
		if t.ID == "" {
			v.add("%s: id is required", tctx)
		} else if seen[t.ID] {
			v.add("%s: duplicate task id %q", tctx, t.ID)
		}
		seen[t.ID] = true

		hasModule := strings.TrimSpace(t.Module) != ""
		hasStructural := len(t.Block) > 0 || t.IncludeTasks != "" || t.ImportTasks != ""
		if !hasModule && !hasStructural {
			v.add("%s: module is required", tctx)
		}
		for _, n := range t.Notify {
			if !handlerNames[n] {
				v.add("%s: notify references unknown handler %q", tctx, n)
			}
		}
		if t.Retries != nil && *t.Retries < 0 {
			v.add("%s: retries must be >= 0", tctx)
		}
		if t.Delay != nil && *t.Delay < 0 {
			v.add("%s: delay must be >= 0", tctx)
		}
	}
}

// Validate checks the inventory.
func (inv *Inventory) Validate() error {
	v := &ValidationError{}
	if inv.ID == "" {
		v.add("inventory %q: id is required", inv.Name)
	}
	if strings.TrimSpace(inv.Name) == "" {
		v.add("inventory: name is required")
	}
	seenGroups := map[string]bool{}
	for _, g := range inv.Groups {
		validateGroup(v, g, seenGroups)
	}
	for _, h := range inv.Hosts {
		validateHost(v, h)
	}
	if len(v.Problems) > 0 {
		return v
	}
	return nil
}

func validateGroup(v *ValidationError, g *InventoryGroup, seen map[string]bool) {
	ctx := fmt.Sprintf("group %q", g.Name)
	if strings.TrimSpace(g.Name) == "" {
		v.add("group: name is required")
	} else if seen[g.Name] {
		v.add("%s: duplicate group name", ctx)
	}
	seen[g.Name] = true
	for _, h := range g.Hosts {
		validateHost(v, h)
	}
	for _, c := range g.Children {
		validateGroup(v, c, seen)
	}
}

func validateHost(v *ValidationError, h *Host) {
	if strings.TrimSpace(h.Name) == "" {
		v.add("host: name is required")
	}
	if h.SSHPort < 0 || h.SSHPort > 65535 {
		v.add("host %q: invalid ssh port %d", h.Name, h.SSHPort)
	}
}

// Validate checks a deployment plan for consistency and bounds.
func (p *DeploymentPlan) Validate() error {
	v := &ValidationError{}
	if p.ProjectID == "" || p.PlaybookID == "" || p.InventoryID == "" {
		v.add("plan: projectId, playbookId and inventoryId are required")
	}
	if p.Verbosity < 0 || p.Verbosity > 4 {
		v.add("plan: verbosity must be between 0 and 4")
	}
	if len(v.Problems) > 0 {
		return v
	}
	return nil
}
