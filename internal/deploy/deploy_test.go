package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotfrankruan/visualible/internal/ansible"
	"github.com/dotfrankruan/visualible/internal/ir"
)

// --- fakes ---

type fakeResolver struct {
	secrets map[string]struct {
		kind   string
		secret string
	}
}

func (f *fakeResolver) ResolveSecret(ctx context.Context, id string) (string, []byte, error) {
	s, ok := f.secrets[id]
	if !ok {
		return "", nil, fmt.Errorf("credential %s not found", id)
	}
	return s.kind, []byte(s.secret), nil
}

type recordedRun struct {
	Args []string
	Dir  string
	Env  []string
}

type fakeRunner struct {
	mu       sync.Mutex
	runs     []recordedRun
	exitCode int
	// stdout, when set, is written to the process stdout (simulating
	// ansible/ansible-playbook output).
	stdout string
	// eventScript, when set, is written to the VISUALIBLE_EVENT_FILE
	// during the run (simulating the callback plugin).
	eventScript []string
}

func (f *fakeRunner) Run(ctx context.Context, opts RunOpts) (int, error) {
	f.mu.Lock()
	f.runs = append(f.runs, recordedRun{Args: opts.Args, Dir: opts.Dir, Env: opts.Env})
	stdout := f.stdout
	f.mu.Unlock()
	if stdout != "" && opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(stdout))
	}
	if len(f.eventScript) > 0 && !strings.Contains(strings.Join(opts.Args, " "), "--syntax-check") {
		for _, env := range opts.Env {
			if path, ok := strings.CutPrefix(env, "VISUALIBLE_EVENT_FILE="); ok {
				content := strings.Join(f.eventScript, "\n") + "\n"
				_ = os.WriteFile(path, []byte(content), 0o600)
			}
		}
	}
	return f.exitCode, nil
}

func testProject() *ir.Project {
	return &ir.Project{
		ID:   "proj1",
		Name: "demo",
		Playbooks: []*ir.Playbook{{
			ID:   "pb1",
			Name: "webserver",
			Plays: []*ir.Play{{
				ID:    "play1",
				Name:  "Configure webserver",
				Hosts: "web",
				Tasks: []*ir.Task{
					{ID: "t1", Name: "Install nginx", Module: "ansible.builtin.apt",
						Args: map[string]any{"name": "nginx", "state": "present"}},
				},
				Handlers: []*ir.Task{
					{ID: "h1", Name: "Restart nginx", Module: "ansible.builtin.systemd_service"},
				},
			}},
		}},
		Inventories: []*ir.Inventory{{
			ID:   "inv1",
			Name: "lab",
			Groups: []*ir.InventoryGroup{{
				ID:   "g1",
				Name: "web",
				Hosts: []*ir.Host{
					{ID: "h1", Name: "node01", Address: "192.168.1.10", CredentialID: "cred1"},
					{ID: "h2", Name: "node02", Address: "192.168.1.11"},
				},
			}},
		}},
	}
}

func testPlan() *ir.DeploymentPlan {
	return &ir.DeploymentPlan{
		ID:          "dep1",
		ProjectID:   "proj1",
		PlaybookID:  "pb1",
		InventoryID: "inv1",
		Check:       true,
		Diff:        true,
		Tags:        []string{"config"},
		Limit:       "node01",
		Verbosity:   2,
	}
}

func testBackend(runner *fakeRunner) *AnsibleBackend {
	inst := &ansible.Installation{PlaybookPath: "/fake/ansible-playbook", Version: "2.16.3"}
	return NewAnsibleBackendWith(inst, runner)
}

func credResolver() *fakeResolver {
	return &fakeResolver{secrets: map[string]struct {
		kind   string
		secret string
	}{
		"cred1": {kind: "ssh_key", secret: "KEY"},
	}}
}

// --- tests ---

func TestValidateRejectsMissingRefs(t *testing.T) {
	b := testBackend(&fakeRunner{})
	plan := testPlan()
	plan.PlaybookID = "nope"
	if err := b.Validate(context.Background(), plan, testProject()); err == nil {
		t.Fatal("expected error for unknown playbook")
	}
}

func TestValidateRejectsEmptyInventory(t *testing.T) {
	b := testBackend(&fakeRunner{})
	project := testProject()
	project.Inventories[0].Groups = nil
	if err := b.Validate(context.Background(), testPlan(), project); err == nil ||
		!strings.Contains(err.Error(), "no hosts") {
		t.Fatalf("expected no-hosts error, got %v", err)
	}
}

func TestValidateRejectsWithoutAnsible(t *testing.T) {
	b := NewAnsibleBackendWith(nil, &fakeRunner{})
	if err := b.Validate(context.Background(), testPlan(), testProject()); err == nil ||
		!strings.Contains(err.Error(), "ansible is not available") {
		t.Fatalf("expected availability error, got %v", err)
	}
}

func TestPrepareWorkspace(t *testing.T) {
	runner := &fakeRunner{}
	b := testBackend(runner)
	resolver := &fakeResolver{secrets: map[string]struct {
		kind   string
		secret string
	}{
		"cred1": {kind: "ssh_key", secret: "PRIVATE KEY MATERIAL"},
	}}

	prepared, err := b.Prepare(context.Background(), testPlan(), testProject(), resolver)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer b.Cleanup(context.Background(), prepared)

	// Workspace permissions.
	fi, err := os.Stat(prepared.Workspace)
	if err != nil {
		t.Fatalf("stat ws: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("workspace perms = %o", fi.Mode().Perm())
	}

	// Playbook rendered, no secrets inside.
	pb, err := os.ReadFile(filepath.Join(prepared.Workspace, "playbook.yml"))
	if err != nil {
		t.Fatalf("read playbook: %v", err)
	}
	if !strings.Contains(string(pb), "ansible.builtin.apt") {
		t.Fatalf("playbook wrong:\n%s", pb)
	}
	if strings.Contains(string(pb), "PRIVATE KEY MATERIAL") {
		t.Fatal("secret leaked into playbook")
	}

	// Inventory references the key file, not the key material.
	inv, err := os.ReadFile(filepath.Join(prepared.Workspace, "inventory.yml"))
	if err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	if !strings.Contains(string(inv), "ansible_ssh_private_key_file") {
		t.Fatalf("inventory missing key file reference:\n%s", inv)
	}
	if strings.Contains(string(inv), "PRIVATE KEY MATERIAL") {
		t.Fatal("secret material leaked into inventory")
	}

	// Key file written with strict permissions.
	keyPath := filepath.Join(prepared.Workspace, "keys", "node01.key")
	ki, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if ki.Mode().Perm() != 0o600 {
		t.Fatalf("key perms = %o", ki.Mode().Perm())
	}

	// Callback plugin + config present.
	for _, f := range []string{"callback_plugins/visualible_events.py", "ansible.cfg", "events.jsonl"} {
		if _, err := os.Stat(filepath.Join(prepared.Workspace, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}

	// Syntax check ran with structured args.
	if len(runner.runs) != 1 {
		t.Fatalf("runs = %d", len(runner.runs))
	}
	joined := strings.Join(runner.runs[0].Args, " ")
	if !strings.Contains(joined, "--syntax-check") {
		t.Fatalf("expected syntax check, args = %v", runner.runs[0].Args)
	}

	// Plan flags flow into execution args.
	data := prepared.BackendData.(*preparedData)
	joined = strings.Join(data.Args, " ")
	for _, want := range []string{"--check", "--diff", "--tags config", "--limit node01", "-vv"} {
		if !strings.Contains(joined, want) {
			t.Errorf("exec args missing %q: %s", want, joined)
		}
	}
}

func TestPrepareFailsOnSyntaxError(t *testing.T) {
	runner := &fakeRunner{exitCode: 4}
	b := testBackend(runner)
	_, err := b.Prepare(context.Background(), testPlan(), testProject(), credResolver())
	if err == nil || !strings.Contains(err.Error(), "syntax-check") {
		t.Fatalf("expected syntax failure, got %v", err)
	}
}

func TestPrepareRejectsUnknownCredentialKind(t *testing.T) {
	b := testBackend(&fakeRunner{})
	resolver := &fakeResolver{secrets: map[string]struct {
		kind   string
		secret string
	}{
		"cred1": {kind: "s3", secret: "x"},
	}}
	_, err := b.Prepare(context.Background(), testPlan(), testProject(), resolver)
	if err == nil || !strings.Contains(err.Error(), "cannot be used for SSH") {
		t.Fatalf("expected kind error, got %v", err)
	}
}

type collectSink struct {
	mu     sync.Mutex
	events []ir.DeploymentEvent
}

func (s *collectSink) Emit(ev ir.DeploymentEvent) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
}

func (s *collectSink) types() []ir.DeploymentEventType {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ir.DeploymentEventType
	for _, e := range s.events {
		out = append(out, e.Type)
	}
	return out
}

func TestExecuteNormalizesEvents(t *testing.T) {
	runner := &fakeRunner{eventScript: []string{
		`{"event":"playbook.start","ts":1700000000.5,"playbook":"playbook.yml"}`,
		`{"event":"play.start","ts":1700000001,"play":"Configure webserver"}`,
		`{"event":"task.start","ts":1700000002,"play":"Configure webserver","task":"Install nginx","is_handler":false}`,
		`{"event":"task.changed","ts":1700000003,"play":"Configure webserver","task":"Install nginx","host":"node01","changed":true}`,
		`{"event":"task.start","ts":1700000004,"play":"Configure webserver","task":"Restart nginx","is_handler":true}`,
		`{"event":"task.ok","ts":1700000005,"task":"Restart nginx","host":"node01"}`,
		`{"event":"stats","ts":1700000006,"summary":{"node01":{"ok":3,"changed":1,"failures":0,"unreachable":0}}}`,
	}}
	b := testBackend(runner)
	prepared, err := b.Prepare(context.Background(), testPlan(), testProject(), credResolver())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer b.Cleanup(context.Background(), prepared)

	sink := &collectSink{}
	code, err := b.Execute(context.Background(), prepared, sink)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	types := sink.types()
	want := []ir.DeploymentEventType{
		ir.EventDeploymentStarted,
		ir.EventPlayStarted,
		ir.EventTaskStarted,
		ir.EventTaskChanged,
		ir.EventHandlerStarted,
		ir.EventTaskOK,
		ir.EventDeploymentFinished,
	}
	if len(types) != len(want) {
		t.Fatalf("event types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event[%d] = %s, want %s (all: %v)", i, types[i], want[i], types)
		}
	}

	// Spot-check fields.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	changed := sink.events[3]
	if changed.Host != "node01" || !changed.Changed || changed.Task != "Install nginx" {
		t.Fatalf("changed event wrong: %+v", changed)
	}
	if changed.Timestamp.Year() != 2023 {
		t.Fatalf("timestamp not taken from callback: %v", changed.Timestamp)
	}
	fin := sink.events[6]
	if !strings.Contains(fin.Message, "node01") || !strings.Contains(fin.Message, "changed=1") {
		t.Fatalf("stats summary wrong: %q", fin.Message)
	}
}

func TestCancelNotRunning(t *testing.T) {
	b := testBackend(&fakeRunner{})
	if err := b.Cancel(context.Background(), "dep-x"); err == nil {
		t.Fatal("expected error for unknown deployment")
	}
}

func TestCleanupRefusesForeignPath(t *testing.T) {
	b := testBackend(&fakeRunner{})
	err := b.Cleanup(context.Background(), &PreparedDeployment{Workspace: "/tmp"})
	if err == nil {
		t.Fatal("expected refusal for foreign workspace")
	}
}

func TestNormalizeCallbackEventSkipsGarbage(t *testing.T) {
	if NormalizeCallbackEvent("d1", []byte("not json")) != nil {
		t.Fatal("expected nil for garbage")
	}
	if NormalizeCallbackEvent("d1", []byte(`{"event":"unknown.thing"}`)) != nil {
		t.Fatal("expected nil for unknown event")
	}
}

func TestManagerLifecycle(t *testing.T) {
	runner := &fakeRunner{eventScript: []string{
		`{"event":"playbook.start","ts":1700000000,"playbook":"playbook.yml"}`,
		`{"event":"task.ok","ts":1700000001,"task":"Install nginx","host":"node01"}`,
	}}
	backend := testBackend(runner)
	m := NewManager(backend, nil, credResolver())

	d, err := m.Start(context.Background(), testPlan(), testProject())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if d.ID != "dep1" {
		t.Fatalf("id = %q", d.ID)
	}

	// Subscribe and collect until the channel closes.
	ch, _, ok := m.Subscribe(d.ID, 16)
	if !ok {
		t.Fatal("subscribe failed")
	}
	deadline := time.After(3 * time.Second)
	var got []ir.DeploymentEvent
	for {
		select {
		case ev, open := <-ch:
			if !open {
				goto drained
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatal("timed out waiting for deployment")
		}
	}
drained:
	if len(got) < 2 {
		t.Fatalf("expected at least 2 events, got %v", got)
	}

	final, ok := m.Get(d.ID)
	if !ok {
		t.Fatal("deployment missing")
	}
	if final.Status != ir.DeploymentSucceeded {
		t.Fatalf("status = %s", final.Status)
	}
	if final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("exit code = %v", final.ExitCode)
	}
}

func TestManagerRejectsInvalidPlan(t *testing.T) {
	backend := testBackend(&fakeRunner{})
	m := NewManager(backend, nil, credResolver())
	plan := testPlan()
	plan.InventoryID = "missing"
	if _, err := m.Start(context.Background(), plan, testProject()); err == nil {
		t.Fatal("expected start rejection")
	}
}

// TestPrepareTargetsTheSelectedPlaybook verifies multi-playbook projects:
// a deployment must render exactly the playbook named by the plan, never
// the first one in the project.
func TestPrepareTargetsTheSelectedPlaybook(t *testing.T) {
	runner := &fakeRunner{}
	b := testBackend(runner)

	project := testProject()
	project.Playbooks = append(project.Playbooks, &ir.Playbook{
		ID: "pb2", Name: "second playbook",
		Plays: []*ir.Play{{
			ID: "play2", Name: "second", Hosts: "db",
			Tasks: []*ir.Task{
				{ID: "t9", Name: "Unique second task", Module: "ansible.builtin.debug",
					Args: map[string]any{"msg": "from-second-playbook"}},
			},
		}},
	})

	plan := testPlan()
	plan.PlaybookID = "pb2"
	prepared, err := b.Prepare(context.Background(), plan, project, credResolver())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer b.Cleanup(context.Background(), prepared)

	rendered, err := os.ReadFile(filepath.Join(prepared.Workspace, "playbook.yml"))
	if err != nil {
		t.Fatalf("read render: %v", err)
	}
	yaml := string(rendered)
	if !strings.Contains(yaml, "from-second-playbook") {
		t.Fatalf("selected playbook not rendered:\n%s", yaml)
	}
	if strings.Contains(yaml, "Install nginx") {
		t.Fatalf("another playbook leaked into the deployment:\n%s", yaml)
	}
	if !strings.Contains(yaml, "hosts: db") {
		t.Fatalf("wrong hosts rendered:\n%s", yaml)
	}
}

// TestPrepareUnknownPlaybookIsRejected: a stale plan (playbook deleted
// after the deployment was queued) fails validation instead of silently
// deploying something else.
func TestPrepareUnknownPlaybookIsRejected(t *testing.T) {
	b := testBackend(&fakeRunner{})
	plan := testPlan()
	plan.PlaybookID = "gone"
	err := b.Validate(context.Background(), plan, testProject())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a clear rejection, got %v", err)
	}
}
