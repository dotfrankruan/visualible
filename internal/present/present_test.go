package present

import (
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/action"
	"github.com/dotfrankruan/visualible/internal/aidiff"
	"github.com/dotfrankruan/visualible/internal/ir"
)

func task(id, name, module string, args map[string]any) *ir.Task {
	return &ir.Task{ID: id, Name: name, Module: module, Args: args}
}

func findDetail(details []action.Detail, label string) *action.Detail {
	for i := range details {
		if details[i].Label == label {
			return &details[i]
		}
	}
	return nil
}

// --- task presentation (the friendly language) ----------------------

func TestPresentPackageInstall(t *testing.T) {
	p := action.Present(task("t1", "Install nginx", "ansible.builtin.package",
		map[string]any{"name": "nginx", "state": "present"}), false)
	if p.Title != "Install nginx" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Advanced {
		t.Fatal("a plain package task must not be Advanced")
	}
	if d := findDetail(p.Details, "Package"); d == nil || d.Value != "nginx" {
		t.Fatalf("package detail = %+v", p.Details)
	}
	if d := findDetail(p.Details, "Ensure"); d == nil || d.Value != "Installed" {
		t.Fatalf("ensure detail = %+v", d)
	}
	for _, d := range p.Details {
		if strings.Contains(d.Value, "ansible.builtin") {
			t.Errorf("module name leaked into friendly details: %+v", d)
		}
	}
}

func TestPresentServiceRunningAndEnabled(t *testing.T) {
	p := action.Present(task("t1", "Start nginx", "ansible.builtin.service",
		map[string]any{"name": "nginx", "state": "started", "enabled": true}), false)
	if p.Title != "Start nginx" {
		t.Fatalf("title = %q", p.Title)
	}
	if d := findDetail(p.Details, "State"); d == nil || d.Value != "Running" {
		t.Fatalf("state detail = %+v", p.Details)
	}
	if d := findDetail(p.Details, "Start automatically after reboot"); d == nil || d.Value != "Yes" {
		t.Fatalf("boot detail = %+v", p.Details)
	}
}

func TestPresentFileLink(t *testing.T) {
	site := map[string]any{
		"state": "link",
		"src":   "/etc/nginx/sites-available/reverse-proxy.conf",
		"dest":  "/etc/nginx/sites-enabled/reverse-proxy.conf",
	}
	p := action.Present(task("t1", "Enable reverse proxy site", "ansible.builtin.file", site), false)
	// A human-written name describes intent better than the derived label.
	if p.Title != "Enable reverse proxy site" {
		t.Fatalf("title = %q", p.Title)
	}
	if d := findDetail(p.Details, "Enables"); d == nil || d.Value != "/etc/nginx/sites-enabled/reverse-proxy.conf" {
		t.Fatalf("enables detail = %+v", p.Details)
	}
	if d := findDetail(p.Details, "Points to"); d == nil || d.Value != "/etc/nginx/sites-available/reverse-proxy.conf" {
		t.Fatalf("points-to detail = %+v", p.Details)
	}
	if p.Advanced {
		t.Fatal("a symlink must be presentable")
	}
	// Without a human-written name the curated label is used instead.
	bare := action.Present(&ir.Task{ID: "t2", Module: "ansible.builtin.file", Args: site}, false)
	if !strings.HasPrefix(bare.Title, "Enable reverse-proxy.conf") {
		t.Fatalf("derived title = %q", bare.Title)
	}
}

func TestPresentFileAbsent(t *testing.T) {
	args := map[string]any{"state": "absent", "path": "/etc/nginx/sites-enabled/default"}
	p := action.Present(task("t1", "Disable default nginx site", "ansible.builtin.file", args), false)
	if p.Title != "Disable default nginx site" {
		t.Fatalf("title = %q", p.Title)
	}
	// A technical/auto-generated name falls back to the friendly label.
	bare := action.Present(&ir.Task{ID: "t2", Module: "ansible.builtin.file", Args: args}, false)
	if !strings.HasPrefix(bare.Title, "Disable") {
		t.Fatalf("derived title = %q", bare.Title)
	}
	if d := findDetail(p.Details, "Removes"); d == nil || d.Value != "/etc/nginx/sites-enabled/default" {
		t.Fatalf("removes detail = %+v", p.Details)
	}
	// Deleting a configuration is a risk worth flagging deterministically.
	if len(p.Risks) == 0 || !strings.Contains(p.Risks[0], "Removes an existing file") {
		t.Fatalf("expected a removal risk hint, got %+v", p.Risks)
	}
}

func TestPresentHandler(t *testing.T) {
	p := action.Present(task("h1", "Restart nginx", "ansible.builtin.service",
		map[string]any{"name": "nginx", "state": "restarted"}), true)
	if p.Title != "Restart nginx when configuration changes" {
		t.Fatalf("handler title = %q", p.Title)
	}
	if !p.Handler {
		t.Fatal("handler flag missing")
	}
	if d := findDetail(p.Details, "Triggered by"); d == nil {
		t.Fatalf("trigger detail missing: %+v", p.Details)
	}
	// The word "handler" is Ansible vocabulary: it belongs to advanced
	// details, not the friendly title.
	if strings.Contains(p.Title, "handler") {
		t.Fatalf("friendly title leaks Ansible vocabulary: %q", p.Title)
	}
}

func TestPresentUnknownModuleIsHonestFallback(t *testing.T) {
	task := task("t1", "Custom thing", "community.vendor.magic_module",
		map[string]any{"endpoint": "https://example.test", "retries": 3})
	p := action.Present(task, false)
	if !p.Advanced {
		t.Fatal("unknown module must fall back to Advanced")
	}
	if p.Title != "Advanced Ansible task" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Module != "community.vendor.magic_module" {
		t.Fatalf("module = %q", p.Module)
	}
	// Technical details must remain complete: every argument is present.
	if d := findDetail(p.Details, "endpoint"); d == nil || d.Value != "https://example.test" {
		t.Fatalf("raw arg missing: %+v", p.Details)
	}
	if d := findDetail(p.Details, "retries"); d == nil {
		t.Fatalf("raw arg missing: %+v", p.Details)
	}
	// No invented friendly description.
	if strings.Contains(p.Title, "Install") || strings.Contains(p.Title, "Configure") {
		t.Fatalf("fallback invented a description: %q", p.Title)
	}
}

func TestPresentLongValueIsSummarized(t *testing.T) {
	content := "server {\n  listen 80;\n  location / {\n" + strings.Repeat("    proxy_pass http://127.0.0.1:8888;\n", 400) + "  }\n}\n"
	p := action.Present(task("t1", "Deploy config", "ansible.builtin.copy", map[string]any{
		"dest": "/etc/nginx/sites-available/reverse-proxy.conf", "content": content,
	}), false)
	d := findDetail(p.Details, "Content")
	if d == nil {
		t.Fatalf("content detail missing: %+v", p.Details)
	}
	if d.Long == nil {
		t.Fatalf("long value was not summarized: %+v", d)
	}
	if d.Value != "" {
		t.Fatalf("summarized value must not also carry the full text: %q", d.Value)
	}
	if d.Long.Lines < 400 {
		t.Fatalf("line count = %d", d.Long.Lines)
	}
	if len(d.Long.Preview) > 210 {
		t.Fatalf("preview too long: %d chars", len(d.Long.Preview))
	}
	if !strings.Contains(d.Long.Preview, "server {") {
		t.Fatalf("preview lost the beginning: %q", d.Long.Preview)
	}
	// The card stays scannable: no detail carries the whole blob.
	for _, det := range p.Details {
		if len(det.Value) > 400 {
			t.Fatalf("detail %q dumped a huge value (%d chars)", det.Label, len(det.Value))
		}
	}
}

func TestPresentRiskHints(t *testing.T) {
	cases := []struct {
		module string
		args   map[string]any
		want   string
	}{
		{"ansible.builtin.user", map[string]any{"name": "debug", "state": "absent"}, "Deletes a user account"},
		{"ansible.posix.authorized_key", map[string]any{"user": "frank", "key": "ssh-ed25519 A"}, "Changes SSH authentication"},
		{"ansible.builtin.package", map[string]any{"name": "nginx", "state": "absent"}, "Removes installed software"},
		{"ansible.builtin.service", map[string]any{"name": "nginx", "state": "restarted"}, "Interrupts a running service"},
		{"ansible.builtin.command", map[string]any{"cmd": "rm -rf /tmp/x"}, "Runs a raw command on the machines"},
		{"community.general.ufw", map[string]any{"rule": "allow", "port": "22"}, "Changes firewall rules"},
		{"ansible.builtin.user", map[string]any{"name": "frank", "groups": "sudo", "append": true}, "Grants administrator privileges"},
	}
	for _, c := range cases {
		risks := action.Risks(task("t", "x", c.module, c.args))
		found := false
		for _, r := range risks {
			if r == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s %v: expected risk %q, got %v", c.module, c.args, c.want, risks)
		}
	}
	// Harmless tasks carry no risk noise.
	if risks := action.Risks(task("t", "x", "ansible.builtin.debug", map[string]any{"msg": "hi"})); len(risks) != 0 {
		t.Errorf("debug task should have no risk hints, got %v", risks)
	}
}

// --- proposal summary wording ---------------------------------------

func TestCountsGrammar(t *testing.T) {
	cases := map[int]string{0: "0 changes", 1: "1 change", 2: "2 changes", 10: "10 changes"}
	for n, want := range cases {
		if got := Count(n, "change"); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
	if got := Count(1, "removal"); got != "1 removal" {
		t.Errorf("Count(1, removal) = %q", got)
	}
}

func TestHeadline(t *testing.T) {
	cases := []struct {
		a, m, r, mv int
		want        string
	}{
		{6, 0, 0, 0, "6 changes proposed"},
		{1, 0, 0, 0, "1 change proposed"},
		{3, 2, 1, 0, "6 changes proposed"},
		{0, 0, 0, 0, "No changes proposed"},
	}
	for _, c := range cases {
		if got := Headline(c.a, c.m, c.r, c.mv); got != c.want {
			t.Errorf("Headline(%d,%d,%d,%d) = %q, want %q", c.a, c.m, c.r, c.mv, got, c.want)
		}
	}
}

func TestSummaryPartsMixedProposal(t *testing.T) {
	parts := SummaryParts(3, 2, 1, 0)
	if len(parts) != 3 {
		t.Fatalf("parts = %+v", parts)
	}
	want := []string{"+ 3 additions", "~ 2 modifications", "− 1 removal"}
	for i, w := range want {
		if parts[i].Label != w {
			t.Errorf("part %d = %q, want %q", i, parts[i].Label, w)
		}
	}
	// No removals -> no removal counter at all.
	parts = SummaryParts(4, 1, 0, 0)
	for _, p := range parts {
		if p.Kind == "removed" {
			t.Fatalf("unexpected removal counter: %+v", parts)
		}
	}
	if got := SummaryParts(4, 1, 0, 0)[0].Label; got != "+ 4 additions" {
		t.Errorf("additions label = %q", got)
	}
}

func TestBulkAndApplyLabels(t *testing.T) {
	if got := BulkAcceptLabel(6); got != "Accept all 6 changes" {
		t.Errorf("BulkAcceptLabel = %q", got)
	}
	if got := BulkAcceptLabel(1); got != "Accept all 1 change" {
		t.Errorf("BulkAcceptLabel(1) = %q", got)
	}
	if got := ApplyLabel(0); got != "Apply selected changes" {
		t.Errorf("ApplyLabel(0) = %q", got)
	}
	if got := ApplyLabel(1); got != "Apply 1 change" {
		t.Errorf("ApplyLabel(1) = %q", got)
	}
	if got := ApplyLabel(3); got != "Apply 3 changes" {
		t.Errorf("ApplyLabel(3) = %q", got)
	}
}

func TestStateLabels(t *testing.T) {
	cases := []struct{ kind, state, want string }{
		{"added", "accepted", "Will be added"},
		{"added", "rejected", "Not added"},
		{"removed", "accepted", "Will be removed"},
		{"removed", "rejected", "Kept as is"},
		{"modified", "accepted", "Will be changed"},
		{"modified", "rejected", "Kept as is"},
		{"moved", "accepted", "Will be moved"},
		{"moved", "rejected", "Kept in place"},
		{"added", "", "Pending decision"},
	}
	for _, c := range cases {
		if got := StateLabel(c.kind, c.state); got != c.want {
			t.Errorf("StateLabel(%s,%s) = %q, want %q", c.kind, c.state, got, c.want)
		}
	}
}

func TestMergeFailureMessage(t *testing.T) {
	raw := `merged playbook would be invalid: [playbook "x" play "y" tasks[0] "Deploy config": notify references unknown handler "Restart nginx"]`
	msg := MergeFailureMessage(raw)
	if !strings.Contains(msg, "Restart nginx") {
		t.Fatalf("message should name the dependency: %q", msg)
	}
	if strings.Contains(msg, "merged playbook would be invalid") {
		t.Fatalf("raw backend wording leaked: %q", msg)
	}
	if !strings.Contains(msg, "cannot be applied") {
		t.Fatalf("message is not explanatory: %q", msg)
	}
	other := MergeFailureMessage("merged playbook would be invalid: [playbook \"x\" play \"y\" tasks[0] \"a\": module is required]")
	if !strings.Contains(other, "without anything to do") {
		t.Fatalf("module-required message = %q", other)
	}
}

// --- field-level review ---------------------------------------------

func TestFieldLabelTranslation(t *testing.T) {
	cases := map[string]string{
		"args.dest":           "Destination",
		"args.src":            "Source",
		"args.mode":           "Permissions",
		"args.enabled":        "Start automatically after reboot",
		"play.become":         "Run with administrator privileges",
		"play.hosts":          "Machines",
		"notify":              "Restarts",
		"args.custom_setting": "Custom Setting",
		"args.nested.value":   "Value (Nested)",
	}
	for path, want := range cases {
		if got := FieldLabel(path); got != want {
			t.Errorf("FieldLabel(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestFieldViewRendersBooleansAndLists(t *testing.T) {
	boolView := fieldView(aidiff.FieldChange{Path: "args.enabled", From: false, To: true})
	if boolView.From != "No" || boolView.To != "Yes" || boolView.ValueKind != "bool" {
		t.Fatalf("boolean view = %+v", boolView)
	}
	listView := fieldView(aidiff.FieldChange{
		Path: "args.ports", From: []any{"22", "80"}, To: []any{"22", "80", "443"},
	})
	if listView.ValueKind != "list" || len(listView.ToList) != 3 {
		t.Fatalf("list view = %+v", listView)
	}
	missing := fieldView(aidiff.FieldChange{Path: "args.owner", To: "root"})
	if missing.From != "(not set)" {
		t.Fatalf("absent value should be named: %+v", missing)
	}
	long := fieldView(aidiff.FieldChange{Path: "args.content", From: "", To: strings.Repeat("line\n", 50)})
	if long.ToLong == nil || long.To != "" {
		t.Fatalf("long value not summarized: %+v", long)
	}
}

// --- full proposal view ----------------------------------------------

func nginxProposal() (*ir.Playbook, *ir.Playbook) {
	base := &ir.Playbook{ID: "pb", Name: "web", Plays: []*ir.Play{{
		ID: "play1", Name: "web", Hosts: "web", Tasks: []*ir.Task{},
	}}}
	proposed := &ir.Playbook{ID: "pb", Name: "web", Plays: []*ir.Play{{
		ID: "play1", Name: "web", Hosts: "web", Tasks: []*ir.Task{
			task("t1", "Install nginx", "ansible.builtin.package", map[string]any{"name": "nginx", "state": "present"}),
			task("t2", "Configure reverse proxy", "ansible.builtin.copy", map[string]any{
				"dest":    "/etc/nginx/sites-available/reverse-proxy.conf",
				"content": "server { listen 80; }",
			}),
			task("t3", "Enable site", "ansible.builtin.file", map[string]any{
				"state": "link",
				"src":   "/etc/nginx/sites-available/reverse-proxy.conf",
				"dest":  "/etc/nginx/sites-enabled/reverse-proxy.conf",
			}),
			task("t4", "Disable default site", "ansible.builtin.file", map[string]any{
				"state": "absent", "path": "/etc/nginx/sites-enabled/default",
			}),
			task("t5", "Start nginx", "ansible.builtin.service", map[string]any{
				"name": "nginx", "state": "started", "enabled": true,
			}),
		},
		Handlers: []*ir.Task{
			task("h1", "Restart nginx", "ansible.builtin.service", map[string]any{
				"name": "nginx", "state": "restarted",
			}),
		},
	}}}
	return base, proposed
}

func TestBuildViewNginxProposal(t *testing.T) {
	base, proposed := nginxProposal()
	d := aidiff.Playbooks(base, proposed)
	v := BuildView(base, proposed, d)

	if v.Additions != 6 {
		t.Fatalf("additions = %d (want 6: 5 tasks + 1 handler)", v.Additions)
	}
	if v.Headline != "6 changes proposed" {
		t.Fatalf("headline = %q", v.Headline)
	}
	if v.HasRemovals {
		t.Fatal("no removals expected")
	}
	if v.BulkAcceptLabel != "Accept all 6 changes" {
		t.Fatalf("bulk label = %q", v.BulkAcceptLabel)
	}

	// A beginner must be able to answer the acceptance questions from the
	// titles and details alone.
	titles := map[string]ChangeView{}
	for _, c := range v.Changes {
		titles[c.Title] = c
	}
	checks := []struct{ title, label, value string }{
		{"Install nginx", "Package", "nginx"},
		{"Disable default site", "Removes", "/etc/nginx/sites-enabled/default"},
		{"Start nginx", "Start automatically after reboot", "Yes"},
		{"Restart nginx when configuration changes", "Triggered by", "nginx configuration changes"},
		{"Configure reverse proxy", "File", "/etc/nginx/sites-available/reverse-proxy.conf"},
	}
	for _, c := range checks {
		view, ok := titles[c.title]
		if !ok {
			t.Errorf("missing change view %q (have %v)", c.title, keysOf(titles))
			continue
		}
		if d := findDetail(view.Details, c.label); d == nil || d.Value != c.value {
			t.Errorf("%s: detail %q = %+v", c.title, c.label, d)
		}
	}

	// Enabling the site must explain that it depends on the config file.
	enable, ok := titles["Enable site"]
	if !ok {
		t.Fatalf("enable view missing (have %v)", keysOf(titles))
	}
	if len(enable.DependsOn) == 0 {
		t.Fatalf("expected a dependency on the configuration step: %+v", enable)
	}
	// The dependency names the step that creates the file, in the same
	// language the rest of the review uses.
	if !strings.Contains(strings.ToLower(enable.DependsOn[0]), "reverse proxy") {
		t.Fatalf("dependency should name the configuration step: %v", enable.DependsOn)
	}

	// No beginner-facing text may leak Ansible mechanics.
	for _, c := range v.Changes {
		for _, text := range []string{c.Title, c.Subtitle, c.AcceptLabel, c.RejectLabel} {
			if strings.Contains(text, "ansible.builtin") || strings.Contains(text, "state=") {
				t.Errorf("Ansible mechanics leaked into %q", text)
			}
		}
		for _, d := range c.Details {
			if strings.Contains(d.Value, "ansible.builtin") {
				t.Errorf("module name leaked into detail %q: %q", d.Label, d.Value)
			}
		}
	}

	// Handlers are presented as reactions, not as steps.
	var handlerViews int
	for _, c := range v.Changes {
		if c.Handler {
			handlerViews++
			if !strings.Contains(c.Title, "when configuration changes") {
				t.Errorf("handler title = %q", c.Title)
			}
		}
	}
	if handlerViews != 1 {
		t.Fatalf("handler views = %d", handlerViews)
	}
}

func TestBuildViewRemovalIsExplicit(t *testing.T) {
	base := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{
			task("t1", "Keep me", "ansible.builtin.package", map[string]any{"name": "curl", "state": "present"}),
			task("t2", "Debug user", "ansible.builtin.user", map[string]any{"name": "debug-admin", "state": "present"}),
		},
	}}}
	proposed := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{
			task("t1", "Keep me", "ansible.builtin.package", map[string]any{"name": "curl", "state": "present"}),
		},
	}}}
	v := BuildView(base, proposed, aidiff.Playbooks(base, proposed))
	if !v.HasRemovals || v.Removals != 1 {
		t.Fatalf("removals not surfaced: %+v", v)
	}
	if !strings.Contains(v.BulkAcceptNote, "1 removal") {
		t.Fatalf("bulk accept must warn about removals: %q", v.BulkAcceptNote)
	}
	if v.BulkAcceptLabel != "Accept all 1 change" {
		t.Fatalf("bulk label = %q", v.BulkAcceptLabel)
	}
	var removal *ChangeView
	for i := range v.Changes {
		if v.Changes[i].Kind == "removed" {
			removal = &v.Changes[i]
		}
	}
	if removal == nil {
		t.Fatal("removal view missing")
	}
	if !removal.Destructive {
		t.Fatal("removal must be flagged destructive")
	}
	if removal.AcceptLabel != "Accept removal" || removal.RejectLabel != "Keep current" {
		t.Fatalf("removal labels = %q / %q", removal.AcceptLabel, removal.RejectLabel)
	}
	if removal.Icon != "−" || removal.KindRank != "REMOVED" {
		t.Fatalf("removal visuals = %q / %q", removal.Icon, removal.KindRank)
	}
	// Current behaviour is described, so the user knows what would be lost.
	if d := findDetail(removal.CurrentDetails, "Username"); d == nil || d.Value != "debug-admin" {
		t.Fatalf("current behaviour details = %+v", removal.CurrentDetails)
	}
}

func TestBuildViewModifiedShowsOnlyWhatChanged(t *testing.T) {
	base := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{task("t1", "Configure", "ansible.builtin.template", map[string]any{
			"src": "n.j2", "dest": "/etc/nginx/nginx.conf", "mode": "0644",
			"proxy_pass": "http://127.0.0.1:8080",
		})},
	}}}
	proposed := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{task("t1", "Configure", "ansible.builtin.template", map[string]any{
			"src": "n.j2", "dest": "/etc/nginx/nginx.conf", "mode": "0644",
			"proxy_pass": "http://127.0.0.1:8888",
		})},
	}}}
	v := BuildView(base, proposed, aidiff.Playbooks(base, proposed))
	if len(v.Changes) != 1 {
		t.Fatalf("changes = %d", len(v.Changes))
	}
	c := v.Changes[0]
	if c.KindRank != "MODIFIED" || c.Icon != "~" {
		t.Fatalf("kind visuals = %+v", c)
	}
	if len(c.FieldChanges) != 1 {
		t.Fatalf("unchanged fields must not be listed: %+v", c.FieldChanges)
	}
	fc := c.FieldChanges[0]
	if fc.Path != "args.proxy_pass" || fc.From != "http://127.0.0.1:8080" || fc.To != "http://127.0.0.1:8888" {
		t.Fatalf("field change = %+v", fc)
	}
	if fc.Label != "Proxy Pass" {
		t.Fatalf("field label = %q", fc.Label)
	}
	// The resulting automation is available on demand, not competing with
	// the change itself.
	if len(c.ResultDetails) == 0 {
		t.Fatal("result details missing for the 'show unchanged fields' affordance")
	}
	if c.AcceptLabel != "Accept change" || c.RejectLabel != "Keep current" {
		t.Fatalf("labels = %q / %q", c.AcceptLabel, c.RejectLabel)
	}
}

func TestBuildViewMovesAreNotAddRemove(t *testing.T) {
	base := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{
			task("t1", "First", "ansible.builtin.debug", map[string]any{"msg": "a"}),
			task("t2", "Second", "ansible.builtin.debug", map[string]any{"msg": "b"}),
		},
	}}}
	proposed := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all",
		Tasks: []*ir.Task{
			task("t2", "Second", "ansible.builtin.debug", map[string]any{"msg": "b"}),
			task("t1", "First", "ansible.builtin.debug", map[string]any{"msg": "a"}),
		},
	}}}
	v := BuildView(base, proposed, aidiff.Playbooks(base, proposed))
	if v.Additions != 0 || v.Deletions != 0 {
		t.Fatalf("move misreported as add/remove: %+v", v)
	}
	if v.Moves != 2 {
		t.Fatalf("moves = %d", v.Moves)
	}
	if v.Changes[0].KindRank != "MOVED" || v.Changes[0].Icon != "↕" {
		t.Fatalf("move visuals = %+v", v.Changes[0])
	}
	if v.Changes[0].AcceptLabel != "Accept move" || v.Changes[0].RejectLabel != "Keep position" {
		t.Fatalf("move labels = %+v", v.Changes[0])
	}
}

func TestBuildViewPlaySettings(t *testing.T) {
	base := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all", Tasks: []*ir.Task{},
	}}}
	proposed := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{
		ID: "p", Name: "x", Hosts: "all", Become: true, Tasks: []*ir.Task{},
	}}}
	v := BuildView(base, proposed, aidiff.Playbooks(base, proposed))
	if len(v.Changes) != 1 || v.Changes[0].Section != "play" {
		t.Fatalf("play change missing: %+v", v.Changes)
	}
	c := v.Changes[0]
	if c.Title != "Automation settings" {
		t.Fatalf("play change title = %q", c.Title)
	}
	if fc := c.FieldChanges[0]; fc.Label != "Run with administrator privileges" || fc.From != "No" || fc.To != "Yes" {
		t.Fatalf("play field = %+v", fc)
	}
}

func TestBuildViewEmptyDiff(t *testing.T) {
	pb := &ir.Playbook{ID: "pb", Name: "x", Plays: []*ir.Play{{ID: "p", Name: "x", Hosts: "all"}}}
	v := BuildView(pb, pb, aidiff.Playbooks(pb, pb))
	if v.Headline != "No changes proposed" {
		t.Fatalf("headline = %q", v.Headline)
	}
	if len(v.Changes) != 0 {
		t.Fatalf("changes = %+v", v.Changes)
	}
	if v.HasRemovals {
		t.Fatal("no removals expected")
	}
}

// TestAcceptanceBeginnersQuestionsFromProposal encodes the phase's
// acceptance test: from the review model alone, a beginner must be able to
// answer the effect questions without reading Ansible mechanics.
func TestAcceptanceBeginnersQuestionsFromProposal(t *testing.T) {
	base := &ir.Playbook{ID: "pb", Name: "web", Plays: []*ir.Play{{
		ID: "play1", Name: "web", Hosts: "web", Tasks: []*ir.Task{},
	}}}
	config := "server {\n  listen 80;\n  server_name example.test;\n  location / {\n    proxy_pass http://127.0.0.1:8888;\n  }\n}\n"
	proposed := &ir.Playbook{ID: "pb", Name: "web", Plays: []*ir.Play{{
		ID: "play1", Name: "web", Hosts: "web", Tasks: []*ir.Task{
			task("t1", "Install nginx", "ansible.builtin.package", map[string]any{"name": "nginx", "state": "present"}),
			task("t2", "Configure nginx reverse proxy", "ansible.builtin.copy", map[string]any{
				"dest": "/etc/nginx/sites-available/reverse-proxy.conf", "content": config,
			}),
			task("t3", "Enable reverse proxy site", "ansible.builtin.file", map[string]any{
				"state": "link",
				"src":   "/etc/nginx/sites-available/reverse-proxy.conf",
				"dest":  "/etc/nginx/sites-enabled/reverse-proxy.conf",
			}),
			task("t4", "Disable default nginx site", "ansible.builtin.file", map[string]any{
				"state": "absent", "path": "/etc/nginx/sites-enabled/default",
			}),
			task("t5", "Ensure nginx is running", "ansible.builtin.service", map[string]any{
				"name": "nginx", "state": "started", "enabled": true,
			}),
			{ID: "t6", Name: "Reload nginx", Module: "ansible.builtin.service",
				Args: map[string]any{"name": "nginx", "state": "reloaded"}, Notify: nil},
		},
		Handlers: []*ir.Task{
			task("h1", "Restart nginx", "ansible.builtin.service", map[string]any{"name": "nginx", "state": "restarted"}),
		},
	}}}

	view := BuildView(base, proposed, aidiff.Playbooks(base, proposed))
	byTitle := map[string]ChangeView{}
	for _, c := range view.Changes {
		byTitle[c.Title] = c
	}

	// What will be installed?
	if c, ok := byTitle["Install nginx"]; !ok || findDetail(c.Details, "Package") == nil {
		t.Error("cannot answer: what will be installed?")
	}
	// Which configuration file will be created, and where does traffic go?
	cfg, ok := byTitle["Configure nginx reverse proxy"]
	if !ok {
		t.Fatalf("configuration step missing (have %v)", keysOf(byTitle))
	}
	if d := findDetail(cfg.Details, "File"); d == nil || d.Value != "/etc/nginx/sites-available/reverse-proxy.conf" {
		t.Error("cannot answer: which configuration file will be created?")
	}
	if d := findDetail(cfg.Details, "Incoming port"); d == nil || d.Value != "80" {
		t.Error("cannot answer: what port will receive traffic?")
	}
	if d := findDetail(cfg.Details, "Forwards traffic to"); d == nil || d.Value != "http://127.0.0.1:8888" {
		t.Error("cannot answer: where will traffic be forwarded?")
	}
	// Will nginx start automatically?
	if c, ok := byTitle["Ensure nginx is running"]; !ok ||
		findDetail(c.Details, "Start automatically after reboot") == nil {
		t.Error("cannot answer: will nginx start automatically?")
	}
	// Will the default site be disabled?
	if c, ok := byTitle["Disable default nginx site"]; !ok ||
		findDetail(c.Details, "Removes") == nil {
		t.Error("cannot answer: will the default site be disabled?")
	}
	// Will nginx restart when configuration changes?
	var handler *ChangeView
	for i := range view.Changes {
		if view.Changes[i].Handler {
			handler = &view.Changes[i]
		}
	}
	if handler == nil || !strings.Contains(handler.Title, "when configuration changes") {
		t.Error("cannot answer: will nginx restart when configuration changes?")
	}

	// ...and none of that required Ansible vocabulary.
	for _, c := range view.Changes {
		joined := c.Title + " " + c.Subtitle
		for _, jargon := range []string{"ansible.builtin", "state=", "notify", "handler:", "enabled="} {
			if strings.Contains(joined, jargon) {
				t.Errorf("beginner-facing copy contains %q: %q", jargon, joined)
			}
		}
	}
}

func TestPresentFileLinkAcceptsPathAlias(t *testing.T) {
	// ansible.builtin.file treats `path` as an alias of `dest`.
	p := action.Present(task("t1", "Enable reverse proxy site", "ansible.builtin.file", map[string]any{
		"state": "link",
		"src":   "/etc/nginx/sites-available/reverse-proxy.conf",
		"path":  "/etc/nginx/sites-enabled/reverse-proxy.conf",
	}), false)
	if p.Advanced {
		t.Fatalf("path-alias symlink must be presentable: %+v", p)
	}
	if d := findDetail(p.Details, "Enables"); d == nil || d.Value != "/etc/nginx/sites-enabled/reverse-proxy.conf" {
		t.Fatalf("enables detail = %+v", p.Details)
	}
}

func TestPresentNotifyAppearsOnce(t *testing.T) {
	task := task("t1", "Deploy config", "ansible.builtin.template", map[string]any{
		"src": "n.j2", "dest": "/etc/nginx/nginx.conf",
	})
	task.Notify = []string{"Reload nginx"}
	p := action.Present(task, false)
	var rows []action.Detail
	for _, d := range p.Details {
		if d.Label == "When this changes" {
			rows = append(rows, d)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one reaction row, got %+v", p.Details)
	}
	if len(rows[0].Values) != 1 || rows[0].Values[0] != "Reload nginx" {
		t.Fatalf("reaction row must name the real handler: %+v", rows[0])
	}
}

func TestPresentConditionExplanation(t *testing.T) {
	// A condition the curated form cannot represent keeps the task
	// Advanced, but the reason is still explained in plain language.
	task := task("t1", "Enable nginx", "ansible.builtin.service",
		map[string]any{"name": "nginx", "state": "started", "enabled": true})
	task.When = "ansible_os_family == 'Debian'"
	p := action.Present(task, false)
	if !p.Advanced {
		t.Fatal("a conditional task must not be shown as a simple form")
	}
	if d := findDetail(p.Details, "Runs"); d == nil || d.Value != "Only on Debian-family machines" {
		t.Fatalf("condition not explained: %+v", p.Details)
	}
	// Unknown expressions are shown verbatim, never paraphrased wrongly.
	odd := task
	odd.When = "custom_fact | default(false)"
	p2 := action.Present(odd, false)
	if d := findDetail(p2.Details, "Only when"); d == nil || d.Value != "custom_fact | default(false)" {
		t.Fatalf("unknown condition must be shown verbatim: %+v", p2.Details)
	}
	// An Advanced task shows its real arguments (nothing hidden) alongside
	// the condition explanation, rather than an interpretation.
	for _, want := range []string{"name", "state", "enabled"} {
		if findDetail(p2.Details, want) == nil {
			t.Errorf("raw argument %q missing from the Advanced card: %+v", want, p2.Details)
		}
	}
	if d := findDetail(p2.Details, "state"); d == nil || d.Value != "started" {
		t.Fatalf("raw state value must be verbatim: %+v", p2.Details)
	}
}

func keysOf(m map[string]ChangeView) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
