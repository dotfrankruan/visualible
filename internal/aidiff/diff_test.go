package aidiff

import (
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
)

func task(id, name, module string, args map[string]any) *ir.Task {
	return &ir.Task{ID: id, Name: name, Module: module, Args: args}
}

func basePlaybook() *ir.Playbook {
	tr := true
	return &ir.Playbook{
		ID:   "pb1",
		Name: "demo",
		Plays: []*ir.Play{{
			ID:    "play1",
			Name:  "Configure webserver",
			Hosts: "web",
			Tasks: []*ir.Task{
				task("t1", "Install nginx", "ansible.builtin.package", map[string]any{"name": "nginx", "state": "present"}),
				{ID: "t2", Name: "Deploy config", Module: "ansible.builtin.template",
					Args:   map[string]any{"src": "nginx.conf.j2", "dest": "/etc/nginx/nginx.conf"},
					Notify: []string{"Restart nginx"}, Become: &tr},
				task("t3", "Start nginx", "ansible.builtin.service", map[string]any{"name": "nginx", "state": "started"}),
			},
			Handlers: []*ir.Task{
				task("h1", "Restart nginx", "ansible.builtin.service", map[string]any{"name": "nginx", "state": "restarted"}),
			},
		}},
	}
}

func find(changes []Change, kind ChangeKind, taskID string) *Change {
	for i := range changes {
		if changes[i].Kind == kind && changes[i].TaskID == taskID {
			return &changes[i]
		}
	}
	return nil
}

func TestDiffUnchanged(t *testing.T) {
	d := Playbooks(basePlaybook(), basePlaybook())
	if d.Summary.Added+d.Summary.Removed+d.Summary.Modified+d.Summary.Moved != 0 {
		t.Fatalf("expected no changes: %+v", d.Summary)
	}
	if d.Summary.Unchanged != 4 {
		t.Fatalf("unchanged = %d, want 4", d.Summary.Unchanged)
	}
}

func TestDiffAdded(t *testing.T) {
	proposed := basePlaybook()
	proposed.Plays[0].Tasks = append(proposed.Plays[0].Tasks,
		task("t4", "Enable nginx", "ansible.builtin.service", map[string]any{"name": "nginx", "enabled": true}))
	d := Playbooks(basePlaybook(), proposed)
	if d.Summary.Added != 1 {
		t.Fatalf("added = %d", d.Summary.Added)
	}
	c := find(d.Changes, Added, "t4")
	if c == nil || c.Name != "Enable nginx" || c.ToPos != 3 {
		t.Fatalf("addition change wrong: %+v", c)
	}
}

func TestDiffRemoved(t *testing.T) {
	proposed := basePlaybook()
	proposed.Plays[0].Tasks = proposed.Plays[0].Tasks[:2]
	d := Playbooks(basePlaybook(), proposed)
	if d.Summary.Removed != 1 {
		t.Fatalf("removed = %d", d.Summary.Removed)
	}
	c := find(d.Changes, Removed, "t3")
	if c == nil || c.Name != "Start nginx" || c.FromPos != 2 {
		t.Fatalf("removal change wrong: %+v", c)
	}
}

func TestDiffModifiedFieldLevel(t *testing.T) {
	proposed := basePlaybook()
	proposed.Plays[0].Tasks[1].Args["dest"] = "/etc/nginx/sites-available/default"
	proposed.Plays[0].Tasks[1].Notify = []string{"Restart nginx", "Reload systemd"}
	proposed.Plays[0].Tasks[0].Args["state"] = "latest"
	d := Playbooks(basePlaybook(), proposed)
	if d.Summary.Modified != 2 {
		t.Fatalf("modified = %d, summary %+v", d.Summary.Modified, d.Summary)
	}

	c1 := find(d.Changes, Modified, "t1")
	if c1 == nil || len(c1.Fields) != 1 {
		t.Fatalf("t1 fields wrong: %+v", c1)
	}
	if c1.Fields[0].Path != "args.state" || c1.Fields[0].From != "present" || c1.Fields[0].To != "latest" {
		t.Fatalf("t1 field change wrong: %+v", c1.Fields[0])
	}

	c2 := find(d.Changes, Modified, "t2")
	if c2 == nil || len(c2.Fields) != 2 {
		t.Fatalf("t2 fields wrong: %+v", c2)
	}
	var dest, notify *FieldChange
	for i := range c2.Fields {
		switch c2.Fields[i].Path {
		case "args.dest":
			dest = &c2.Fields[i]
		case "notify":
			notify = &c2.Fields[i]
		}
	}
	if dest == nil || dest.To != "/etc/nginx/sites-available/default" {
		t.Fatalf("dest change missing: %+v", c2.Fields)
	}
	if notify == nil {
		t.Fatalf("notify change missing: %+v", c2.Fields)
	}
}

func TestDiffNestedArgs(t *testing.T) {
	base := &ir.Playbook{ID: "p", Name: "x", Plays: []*ir.Play{{ID: "pl", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{task("t1", "c", "m.mod", map[string]any{
			"networks": map[string]any{"name": "front", "ipv4": "10.0.0.5"}})}}}}
	proposed := &ir.Playbook{ID: "p", Name: "x", Plays: []*ir.Play{{ID: "pl", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{task("t1", "c", "m.mod", map[string]any{
			"networks": map[string]any{"name": "front", "ipv4": "10.0.0.9"}})}}}}
	d := Playbooks(base, proposed)
	c := find(d.Changes, Modified, "t1")
	if c == nil || len(c.Fields) != 1 || c.Fields[0].Path != "args.networks.ipv4" {
		t.Fatalf("nested diff wrong: %+v", c)
	}
}

func TestDiffMovedNotAddDelete(t *testing.T) {
	proposed := basePlaybook()
	tasks := proposed.Plays[0].Tasks
	tasks[0], tasks[2] = tasks[2], tasks[0]
	d := Playbooks(basePlaybook(), proposed)
	if d.Summary.Added != 0 || d.Summary.Removed != 0 {
		t.Fatalf("move misdetected as add/remove: %+v", d.Summary)
	}
	if d.Summary.Moved != 2 {
		t.Fatalf("moved = %d, want 2 (%+v)", d.Summary.Moved, d.Summary)
	}
	c := find(d.Changes, Moved, "t1")
	if c == nil || c.FromPos != 0 || c.ToPos != 2 {
		t.Fatalf("move wrong: %+v", c)
	}
}

func TestDiffPlayFields(t *testing.T) {
	proposed := basePlaybook()
	proposed.Plays[0].Hosts = "production"
	proposed.Plays[0].Become = true
	d := Playbooks(basePlaybook(), proposed)
	if len(d.PlayChanges) != 2 {
		t.Fatalf("play changes = %+v", d.PlayChanges)
	}
}

func TestMergeAcceptAll(t *testing.T) {
	base := basePlaybook()
	proposed := basePlaybook()
	proposed.Plays[0].Tasks[0].Args["state"] = "latest"
	proposed.Plays[0].Tasks = append(proposed.Plays[0].Tasks,
		task("t4", "Enable nginx", "ansible.builtin.service", map[string]any{"name": "nginx", "enabled": true}))

	d := Playbooks(base, proposed)
	accepted := map[string]bool{}
	for _, c := range d.Changes {
		accepted[c.ID] = true
	}
	merged, err := Merge(base, proposed, d, accepted)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	tasks := merged.Plays[0].Tasks
	if len(tasks) != 4 {
		t.Fatalf("merged tasks = %d", len(tasks))
	}
	if tasks[0].Args["state"] != "latest" {
		t.Fatalf("modification not applied: %+v", tasks[0])
	}
	if tasks[3].ID != "t4" {
		t.Fatalf("addition not applied: %+v", tasks[3])
	}
}

func TestMergeSelective(t *testing.T) {
	base := basePlaybook()
	proposed := basePlaybook()
	proposed.Plays[0].Tasks[0].Args["state"] = "latest" // modification
	proposed.Plays[0].Tasks = append(proposed.Plays[0].Tasks,
		task("t4", "Enable nginx", "ansible.builtin.service", map[string]any{"name": "nginx", "enabled": true}))
	proposed.Plays[0].Tasks = proposed.Plays[0].Tasks[1:] // removal of t1... wait no

	d := Playbooks(base, proposed)
	// Accept only the addition; reject the modification.
	var addID string
	for _, c := range d.Changes {
		if c.Kind == Added {
			addID = c.ID
		}
	}
	accepted := map[string]bool{addID: true}
	merged, err := Merge(base, proposed, d, accepted)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var t1 *ir.Task
	for _, tk := range merged.Plays[0].Tasks {
		if tk.ID == "t1" {
			t1 = tk
		}
	}
	if t1 == nil {
		t.Fatal("t1 lost")
	}
	if t1.Args["state"] != "present" {
		t.Fatalf("rejected modification leaked: %+v", t1)
	}
}

func TestMergeRejectedRemovalKeepsTask(t *testing.T) {
	base := basePlaybook()
	proposed := basePlaybook()
	proposed.Plays[0].Tasks = proposed.Plays[0].Tasks[:2]

	d := Playbooks(base, proposed)
	merged, err := Merge(base, proposed, d, map[string]bool{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(merged.Plays[0].Tasks) != 3 {
		t.Fatalf("rejected removal should keep task: %d", len(merged.Plays[0].Tasks))
	}
}

func TestMergeDependencyBlocked(t *testing.T) {
	base := basePlaybook()
	proposed := basePlaybook()
	// AI removes the handler but keeps the notify reference.
	proposed.Plays[0].Handlers = nil

	d := Playbooks(base, proposed)
	var removalID string
	for _, c := range d.Changes {
		if c.Kind == Removed && c.Section == "handlers" {
			removalID = c.ID
		}
	}
	if removalID == "" {
		t.Fatal("handler removal not detected")
	}
	_, err := Merge(base, proposed, d, map[string]bool{removalID: true})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected merge to be blocked by validation, got %v", err)
	}
	if !strings.Contains(err.Error(), "unknown handler") {
		t.Fatalf("expected dependency explanation, got %v", err)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	base := basePlaybook()
	proposed := basePlaybook()
	proposed.Plays[0].Tasks[0].Args["state"] = "latest"
	d := Playbooks(base, proposed)
	accepted := map[string]bool{}
	for _, c := range d.Changes {
		accepted[c.ID] = true
	}
	if _, err := Merge(base, proposed, d, accepted); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if base.Plays[0].Tasks[0].Args["state"] != "present" {
		t.Fatal("base mutated")
	}
}

func TestMergeMoveRejectedKeepsBasePosition(t *testing.T) {
	base := basePlaybook()
	proposed := basePlaybook()
	tasks := proposed.Plays[0].Tasks
	tasks[0], tasks[2] = tasks[2], tasks[0]

	d := Playbooks(base, proposed)
	merged, err := Merge(base, proposed, d, map[string]bool{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.Plays[0].Tasks[0].ID != "t1" {
		t.Fatalf("rejected move changed order: %+v", merged.Plays[0].Tasks)
	}
}

func TestHashIRStable(t *testing.T) {
	h1, err := HashIR(basePlaybook())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h2, _ := HashIR(basePlaybook())
	if h1 != h2 {
		t.Fatalf("hash unstable: %s vs %s", h1, h2)
	}
	mod := basePlaybook()
	mod.Plays[0].Tasks[0].Args["state"] = "latest"
	h3, _ := HashIR(mod)
	if h3 == h1 {
		t.Fatal("hash did not change with content")
	}
}
