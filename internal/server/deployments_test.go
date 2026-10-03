package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dotfrankruan/visualible/internal/ansible"
	"github.com/dotfrankruan/visualible/internal/deploy"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/store"
)

// fakeRunner records invocations and optionally writes callback events.
type fakeRunner struct {
	exitCode int
	events   []string
	lastArgs []string
	// stdout is written to process stdout (simulates ansible output).
	stdout string
}

func (f *fakeRunner) Run(ctx context.Context, opts deploy.RunOpts) (int, error) {
	f.lastArgs = opts.Args
	if f.stdout != "" && opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(f.stdout))
	}
	if len(f.events) > 0 && !strings.Contains(strings.Join(opts.Args, " "), "--syntax-check") {
		for _, env := range opts.Env {
			if path, ok := strings.CutPrefix(env, "VISUALIBLE_EVENT_FILE="); ok {
				_ = os.WriteFile(path, []byte(strings.Join(f.events, "\n")+"\n"), 0o600)
			}
		}
	}
	return f.exitCode, nil
}

type fakeSecrets struct{}

func (fakeSecrets) ResolveSecret(ctx context.Context, id string) (string, []byte, error) {
	return "ssh_key", []byte("KEY"), nil
}

func newDeployTestServer(t *testing.T, runner *fakeRunner) *httptest.Server {
	t.Helper()
	st, err := store.Open(":memory:", "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	backend := deploy.NewAnsibleBackendWith(
		&ansible.Installation{PlaybookPath: "/fake/ansible-playbook"}, runner)
	manager := deploy.NewManager(backend, st, fakeSecrets{})
	srv, err := New(fixtureDiscovery(t), st, manager)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Seed a deployable project.
	p := &ir.Project{
		ID:   "proj1",
		Name: "demo",
		Playbooks: []*ir.Playbook{{
			ID: "pb1", Name: "web",
			Plays: []*ir.Play{{
				ID: "play1", Name: "p", Hosts: "web",
				Tasks: []*ir.Task{{ID: "t1", Name: "x", Module: "ansible.builtin.debug"}},
			}},
		}},
		Inventories: []*ir.Inventory{{
			ID: "inv1", Name: "lab",
			Groups: []*ir.InventoryGroup{{
				ID: "g1", Name: "web",
				Hosts: []*ir.Host{{ID: "h1", Name: "node01"}},
			}},
		}},
	}
	if err := st.SaveProject(context.Background(), p); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return ts
}

func TestBackendInfo(t *testing.T) {
	ts := newDeployTestServer(t, &fakeRunner{})
	var bi backendInfoResponse
	getJSON(t, ts.URL+"/api/deploy/backend", http.StatusOK, &bi)
	if bi.Metadata.ID != "ansible-ssh" {
		t.Fatalf("backend = %q", bi.Metadata.ID)
	}
	if !bi.Capabilities.SupportsCheckMode || !bi.Capabilities.SupportsStreaming || bi.Capabilities.RequiresAgent {
		t.Fatalf("capabilities wrong: %+v", bi.Capabilities)
	}
}

func TestDeploymentLifecycle(t *testing.T) {
	runner := &fakeRunner{events: []string{
		`{"event":"playbook.start","ts":1700000000,"playbook":"playbook.yml"}`,
		`{"event":"play.start","ts":1700000001,"play":"p"}`,
		`{"event":"task.ok","ts":1700000002,"task":"x","host":"node01"}`,
		`{"event":"stats","ts":1700000003,"summary":{"node01":{"ok":1}}}`,
	}}
	ts := newDeployTestServer(t, runner)

	// Create.
	resp, err := http.Post(ts.URL+"/api/deployments", "application/json",
		bytes.NewReader([]byte(`{"projectId":"proj1","playbookId":"pb1","inventoryId":"inv1","check":true}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var d ir.Deployment
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	// Wait for completion.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var cur ir.Deployment
		getJSON(t, ts.URL+"/api/deployments/"+d.ID, http.StatusOK, &cur)
		if cur.Status == ir.DeploymentSucceeded || cur.Status == ir.DeploymentFailed {
			if cur.Status != ir.DeploymentSucceeded {
				t.Fatalf("deployment failed: %+v", cur)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deployment did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// List shows it.
	var list struct {
		Deployments []ir.Deployment `json:"deployments"`
	}
	getJSON(t, ts.URL+"/api/deployments", http.StatusOK, &list)
	if len(list.Deployments) != 1 {
		t.Fatalf("deployments = %d", len(list.Deployments))
	}

	// SSE replay delivers stored events (read until we have them all).
	ereq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/deployments/"+d.ID+"/events", nil)
	eresp, err := http.DefaultClient.Do(ereq)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer eresp.Body.Close()
	var body strings.Builder
	wants := []string{"deployment.started", "play.started", "task.ok", "deployment.finished"}
	sseDeadline := time.Now().Add(2 * time.Second)
	buf := make([]byte, 4096)
	for time.Now().Before(sseDeadline) {
		n, rerr := eresp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
		}
		all := true
		for _, want := range wants {
			if !strings.Contains(body.String(), want) {
				all = false
			}
		}
		if all || rerr != nil {
			break
		}
	}
	for _, want := range wants {
		if !strings.Contains(body.String(), want) {
			t.Errorf("SSE replay missing %q\n%s", want, body.String())
		}
	}

	// Syntax check ran before execution.
	if !strings.Contains(strings.Join(runner.lastArgs, " "), "--check") {
		t.Fatalf("expected --check in exec args: %v", runner.lastArgs)
	}
}

func TestDeploymentCreateValidation(t *testing.T) {
	ts := newDeployTestServer(t, &fakeRunner{})

	// Unknown project.
	resp, err := http.Post(ts.URL+"/api/deployments", "application/json",
		bytes.NewReader([]byte(`{"projectId":"nope","playbookId":"pb1","inventoryId":"inv1"}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project status = %d", resp.StatusCode)
	}

	// Invalid plan (bad verbosity).
	resp, err = http.Post(ts.URL+"/api/deployments", "application/json",
		bytes.NewReader([]byte(`{"projectId":"proj1","playbookId":"pb1","inventoryId":"inv1","verbosity":9}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad plan status = %d", resp.StatusCode)
	}

	// Cancel non-running deployment.
	resp, err = http.Post(ts.URL+"/api/deployments/dep-nope/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel status = %d", resp.StatusCode)
	}
}
