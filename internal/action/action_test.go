package action

import (
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/render"
)

func gen(t *testing.T, id string, params map[string]any) *Generated {
	t.Helper()
	g, err := Generate(id, params)
	if err != nil {
		t.Fatalf("generate %s: %v", id, err)
	}
	return g
}

// --- Definitions ---

func TestDefinitionsComplete(t *testing.T) {
	defs := Definitions()
	if len(defs) < 8 {
		t.Fatalf("expected at least 8 curated actions, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		if d.ID == "" || d.Name == "" || d.Icon == "" || d.Summary == "" {
			t.Fatalf("incomplete definition: %+v", d)
		}
		if names[d.Name] {
			t.Fatalf("duplicate action name %q", d.Name)
		}
		names[d.Name] = true
		// Every field needs an id, label, type and (for selects) choices.
		for _, f := range d.Fields {
			if f.ID == "" || f.Label == "" || f.Type == "" {
				t.Fatalf("incomplete field in %s: %+v", d.ID, f)
			}
			if f.Type == "select" && len(f.Choices) == 0 {
				t.Fatalf("select field %s.%s has no choices", d.ID, f.ID)
			}
		}
	}
	// Docker must be gated on its collection.
	for _, d := range defs {
		if d.ID == "docker-container" && d.RequiresCollection != "community.docker" {
			t.Fatalf("docker action not gated: %+v", d)
		}
	}
}

func TestUnknownAction(t *testing.T) {
	if _, err := Generate("nope", nil); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// --- Generation: beginner forms map to correct module arguments ---

func TestGenerateInstallSoftware(t *testing.T) {
	g := gen(t, "install-software", map[string]any{"package": "nginx", "state": "present"})
	if len(g.Tasks) != 1 {
		t.Fatalf("tasks = %d", len(g.Tasks))
	}
	task := g.Tasks[0]
	if task.Module != "ansible.builtin.package" {
		t.Fatalf("module = %q", task.Module)
	}
	if task.Args["name"] != "nginx" || task.Args["state"] != "present" {
		t.Fatalf("args = %+v", task.Args)
	}
	if task.Become == nil || !*task.Become {
		t.Fatal("package installation must require administrator privileges")
	}
	if task.Name != "Install nginx" {
		t.Fatalf("name = %q", task.Name)
	}
	if task.ID == "" {
		t.Fatal("task id missing")
	}
}

func TestGenerateInstallSoftwareCompound(t *testing.T) {
	// The spec's "set up nginx" case: one user intent, two IR tasks.
	g := gen(t, "install-software", map[string]any{
		"package": "nginx", "state": "present",
		"serviceName": "nginx", "ensureRunning": true, "enableBoot": true,
	})
	if len(g.Tasks) != 2 {
		t.Fatalf("expected compound generation, got %d tasks", len(g.Tasks))
	}
	svc := g.Tasks[1]
	if svc.Module != "ansible.builtin.service" {
		t.Fatalf("service module = %q", svc.Module)
	}
	if svc.Args["name"] != "nginx" || svc.Args["state"] != "started" || svc.Args["enabled"] != true {
		t.Fatalf("service args = %+v", svc.Args)
	}
	if g.Tasks[0].ID == g.Tasks[1].ID {
		t.Fatal("compound tasks must have distinct ids")
	}
}

func TestGenerateInstallSoftwareEnableOnly(t *testing.T) {
	g := gen(t, "install-software", map[string]any{
		"package": "nginx", "serviceName": "nginx", "enableBoot": true,
	})
	if len(g.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(g.Tasks))
	}
	if _, hasState := g.Tasks[1].Args["state"]; hasState {
		t.Fatalf("enable-only task should not set state: %+v", g.Tasks[1].Args)
	}
	if g.Tasks[1].Args["enabled"] != true {
		t.Fatalf("enabled missing: %+v", g.Tasks[1].Args)
	}
}

func TestGenerateValidation(t *testing.T) {
	cases := []struct {
		action string
		params map[string]any
		want   string
	}{
		{"install-software", map[string]any{}, "Package name is required."},
		{"install-software", map[string]any{"package": "nginx", "serviceName": "nginx"}, "Choose what to do"},
		{"manage-service", map[string]any{}, "Service name is required."},
		{"create-user", map[string]any{}, "Username is required."},
		{"add-ssh-key", map[string]any{"username": "frank"}, "required"},
		{"deploy-file", map[string]any{"dest": "/tmp/x"}, "File content is required."},
		{"deploy-file", map[string]any{"source": "path", "dest": "/tmp/x"}, "Choose a file to copy."},
		{"deploy-file", map[string]any{"content": "x"}, "Destination path is required."},
		{"render-template", map[string]any{"dest": "/etc/x"}, "required"},
		{"create-directory", map[string]any{}, "Folder path is required."},
		{"run-command", map[string]any{}, "Command is required."},
		{"docker-container", map[string]any{"name": "web"}, "image are required"},
	}
	for _, c := range cases {
		_, err := Generate(c.action, c.params)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s(%v): expected %q, got %v", c.action, c.params, c.want, err)
		}
	}
}

func TestGenerateRenderTemplateCreatesHandler(t *testing.T) {
	g := gen(t, "render-template", map[string]any{
		"templatePath": "nginx.conf.j2", "dest": "/etc/nginx/nginx.conf", "restartService": "nginx",
	})
	if len(g.Tasks) != 1 || len(g.Handlers) != 1 {
		t.Fatalf("tasks/handlers = %d/%d", len(g.Tasks), len(g.Handlers))
	}
	if len(g.Tasks[0].Notify) != 1 || g.Tasks[0].Notify[0] != "Restart nginx" {
		t.Fatalf("notify = %v", g.Tasks[0].Notify)
	}
	h := g.Handlers[0]
	if h.Name != "Restart nginx" || h.Args["state"] != "restarted" {
		t.Fatalf("handler = %+v", h)
	}
	// The generated pair must render and validate as a playbook.
	pb := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all", Tasks: g.Tasks, Handlers: g.Handlers,
	}}}
	if err := pb.Validate(); err != nil {
		t.Fatalf("generated playbook invalid: %v", err)
	}
	if _, err := render.Playbook(pb); err != nil {
		t.Fatalf("generated playbook does not render: %v", err)
	}
}

func TestGenerateCreateUserCompound(t *testing.T) {
	g := gen(t, "create-user", map[string]any{
		"username": "frank", "shell": "/bin/bash", "admin": true,
		"sshKey": "ssh-ed25519 AAAA frank@laptop",
	})
	if len(g.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(g.Tasks))
	}
	if g.Tasks[0].Args["groups"] != "sudo" || g.Tasks[0].Args["append"] != true {
		t.Fatalf("admin args = %+v", g.Tasks[0].Args)
	}
	if g.Tasks[1].Module != "ansible.posix.authorized_key" {
		t.Fatalf("key module = %q", g.Tasks[1].Module)
	}
}

func TestGenerateDockerPorts(t *testing.T) {
	g := gen(t, "docker-container", map[string]any{
		"name": "web", "image": "nginx:latest", "ports": "8080:80, 443:443", "state": "started",
	})
	ports, ok := g.Tasks[0].Args["ports"].([]string)
	if !ok || len(ports) != 2 || ports[0] != "8080:80" {
		t.Fatalf("ports = %+v", g.Tasks[0].Args["ports"])
	}
}

// --- Recognition: IR -> Action ---

func TestRecognizeRoundTrip(t *testing.T) {
	cases := []struct {
		action string
		params map[string]any
		label  string
	}{
		{"install-software", map[string]any{"package": "nginx", "state": "present"}, "Install nginx"},
		{"install-software", map[string]any{"package": "nginx", "state": "absent"}, "Remove nginx"},
		{"manage-service", map[string]any{"service": "nginx", "state": "restarted"}, "Restart nginx"},
		{"create-user", map[string]any{"username": "frank", "admin": true}, "Create user frank"},
		{"add-ssh-key", map[string]any{"username": "frank", "key": "ssh-ed25519 AAAA"}, "Add SSH key for frank"},
		{"deploy-file", map[string]any{"dest": "/etc/nginx/nginx.conf", "content": "x"}, "Deploy nginx.conf"},
		{"render-template", map[string]any{"templatePath": "n.j2", "dest": "/etc/nginx/nginx.conf"}, "Deploy nginx.conf"},
		{"create-directory", map[string]any{"path": "/srv/data"}, "Create folder /srv/data"},
		{"docker-container", map[string]any{"name": "web", "image": "nginx"}, "Run container web"},
	}
	for _, c := range cases {
		g := gen(t, c.action, c.params)
		// Every generated task must be recognizable (no data loss).
		task := g.Tasks[0]
		rec := RecognizeOne(task)
		if !rec.Recognized {
			t.Errorf("%s: generated task not recognized as an action: %+v", c.action, task)
			continue
		}
		if rec.Action != c.action {
			t.Errorf("%s: recognized as %s", c.action, rec.Action)
		}
		if rec.Label != c.label {
			t.Errorf("%s: label = %q, want %q", c.action, rec.Label, c.label)
		}
		if rec.Subtitle == "" {
			t.Errorf("%s: subtitle empty", c.action)
		}
		// Curated params must regenerate equivalent module arguments.
		g2 := gen(t, rec.Action, rec.Params)
		if g2.Tasks[0].Module != task.Module {
			t.Errorf("%s: module drifted %s -> %s", c.action, task.Module, g2.Tasks[0].Module)
		}
		for k, v := range task.Args {
			if g2.Tasks[0].Args[k] != v {
				t.Errorf("%s: arg %s drifted %v -> %v", c.action, k, v, g2.Tasks[0].Args[k])
			}
		}
	}
}

func TestRecognizeRunCommand(t *testing.T) {
	rec := RecognizeOne(&ir.Task{ID: "t1", Module: "ansible.builtin.command",
		Args: map[string]any{"cmd": "uptime"}})
	if !rec.Recognized || rec.Action != "run-command" {
		t.Fatalf("command not recognized: %+v", rec)
	}
	if rec.Params["command"] != "uptime" {
		t.Fatalf("params = %+v", rec.Params)
	}

	rec = RecognizeOne(&ir.Task{ID: "t2", Module: "ansible.builtin.shell",
		Args: map[string]any{"_raw_params": "echo hi | tee /tmp/x"}})
	if !rec.Recognized || rec.Params["useShell"] != true {
		t.Fatalf("shell not recognized with useShell: %+v", rec)
	}
}

// --- Fallback: anything not faithfully representable stays Advanced ---

func TestRecognitionFallsBackOnAdvancedOptions(t *testing.T) {
	base := func() *ir.Task {
		return &ir.Task{ID: "t1", Name: "Install nginx", Module: "ansible.builtin.package",
			Args: map[string]any{"name": "nginx", "state": "present"}}
	}
	if !RecognizeOne(base()).Recognized {
		t.Fatal("plain task should be recognized")
	}

	cases := map[string]func(*ir.Task){
		"when":         func(t *ir.Task) { t.When = "ansible_os_family == 'Debian'" },
		"loop":         func(t *ir.Task) { t.Loop = []any{"a", "b"} },
		"register":     func(t *ir.Task) { t.Register = "pkg" },
		"tags":         func(t *ir.Task) { t.Tags = []string{"setup"} },
		"delegate_to":  func(t *ir.Task) { t.DelegateTo = "localhost" },
		"run_once":     func(t *ir.Task) { t.RunOnce = true },
		"changed_when": func(t *ir.Task) { t.ChangedWhen = "false" },
		"failed_when":  func(t *ir.Task) { t.FailedWhen = "false" },
		"until":        func(t *ir.Task) { t.Until = "result.ok" },
		"extras":       func(t *ir.Task) { t.Extras = map[string]any{"become_user": "root"} },
		"unknown_arg":  func(t *ir.Task) { t.Args["update_cache"] = true },
		"notify":       func(t *ir.Task) { t.Notify = []string{"Restart nginx"} },
		"other_module": func(t *ir.Task) { t.Module = "ansible.builtin.snap" },
	}
	for name, mutate := range cases {
		task := base()
		mutate(task)
		if rec := RecognizeOne(task); rec.Recognized {
			t.Errorf("%s: should NOT be recognized (would hide configuration): %+v", name, rec)
		}
	}
}

func TestRecognitionTemplateAllowsNotify(t *testing.T) {
	task := &ir.Task{ID: "t1", Name: "Deploy config", Module: "ansible.builtin.template",
		Args:   map[string]any{"src": "n.j2", "dest": "/etc/nginx/nginx.conf"},
		Notify: []string{"Restart nginx"}}
	rec := RecognizeOne(task)
	if !rec.Recognized {
		t.Fatalf("template with notify should be recognized: %+v", rec)
	}
	if rec.Params["restartService"] != "nginx" {
		t.Fatalf("restartService = %v", rec.Params["restartService"])
	}
	// But two notified handlers cannot be represented by one curated field.
	task.Notify = []string{"Restart nginx", "Reload systemd"}
	if rec := RecognizeOne(task); rec.Recognized {
		t.Fatalf("multiple notify targets should fall back: %+v", rec)
	}
}

func TestRecognizePackageListFallback(t *testing.T) {
	one := &ir.Task{ID: "t", Module: "ansible.builtin.apt",
		Args: map[string]any{"name": []any{"nginx"}, "state": "present"}}
	if rec := RecognizeOne(one); !rec.Recognized || rec.Params["package"] != "nginx" {
		t.Fatalf("single-element list should be recognized: %+v", rec)
	}
	many := &ir.Task{ID: "t", Module: "ansible.builtin.apt",
		Args: map[string]any{"name": []any{"nginx", "curl"}, "state": "present"}}
	if rec := RecognizeOne(many); rec.Recognized {
		t.Fatalf("multi-package list should fall back to Advanced: %+v", rec)
	}
}

func TestRecognizeUnrecognizedShape(t *testing.T) {
	task := &ir.Task{ID: "t1", Module: "ansible.builtin.debug", Args: map[string]any{"msg": "hi"}}
	rec := RecognizeOne(task)
	if rec.Recognized || rec.TaskID != "t1" {
		t.Fatalf("unexpected recognition: %+v", rec)
	}
}

func TestRecognizeAllPreservesOrder(t *testing.T) {
	tasks := []*ir.Task{
		{ID: "a", Module: "ansible.builtin.package", Args: map[string]any{"name": "nginx"}},
		{ID: "b", Module: "ansible.builtin.debug", Args: map[string]any{"msg": "x"}},
	}
	recs := RecognizeAll(tasks)
	if len(recs) != 2 || recs[0].TaskID != "a" || recs[1].TaskID != "b" {
		t.Fatalf("recognitions = %+v", recs)
	}
	if !recs[0].Recognized || recs[1].Recognized {
		t.Fatalf("unexpected recognition flags: %+v", recs)
	}
}
