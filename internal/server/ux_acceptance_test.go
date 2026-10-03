package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotfrankruan/visualible/internal/action"
	"github.com/dotfrankruan/visualible/internal/ansible"
	"github.com/dotfrankruan/visualible/internal/deploy"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/store"
)

// uxTestServer builds a server with a fake Ansible runner so the full
// new-user journey can be exercised over HTTP without a real SSH target.
func uxTestServer(t *testing.T, runner *fakeRunner) *httptest.Server {
	t.Helper()
	st, err := store.Open(":memory:", "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// Both binaries are pointed at the fake runner: ansible-doc calls go to
	// the fixture discovery, everything executed goes through runner.
	backend := deploy.NewAnsibleBackendWith(&ansible.Installation{
		Path:           "/fake/ansible",
		AnsibleDocPath: "/fake/ansible-doc",
		PlaybookPath:   "/fake/ansible-playbook",
	}, runner)
	manager := deploy.NewManager(backend, st, st)
	srv, err := New(fixtureDiscovery(t), st, manager)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, url, body string, out any) int {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func putJSON(t *testing.T, url, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

// TestUXAcceptanceBeginnerJourney is the critical usability acceptance
// test from the redesign, verified at the API level:
//
//	Install nginx on a Debian server and make sure it starts
//	automatically — without knowing modules, plays, tasks, handlers,
//	inventory, YAML or 'become'.
//
// Every step uses only beginner-facing endpoints; Ansible terminology
// appears only in the optional YAML/advanced views.
func TestUXAcceptanceBeginnerJourney(t *testing.T) {
	// The fake runner reports a successful Ansible ping.
	runner := &fakeRunner{
		exitCode: 0,
		stdout:   `web01 | SUCCESS => {"ansible_facts": {}, "changed": false, "ping": "pong"}`,
	}
	ts := uxTestServer(t, runner)

	// 1. Create project — no Ansible vocabulary involved.
	var project ir.Project
	if code := postJSON(t, ts.URL+"/api/projects", `{"name":"My servers"}`, &project); code != http.StatusCreated {
		t.Fatalf("create project status = %d", code)
	}
	if project.ID == "" || len(project.Playbooks) != 1 {
		t.Fatalf("unexpected project: %+v", project)
	}

	// 2./3./4. "Install software" -> package nginx, keep it running.
	var gen action.Generated
	genBody := `{"action":"install-software","params":{"package":"nginx","state":"present","serviceName":"nginx","ensureRunning":true,"enableBoot":true}}`
	if code := postJSON(t, ts.URL+"/api/actions/generate", genBody, &gen); code != http.StatusOK {
		t.Fatalf("generate status = %d", code)
	}
	if len(gen.Tasks) != 2 {
		t.Fatalf("one user intent should produce two IR tasks, got %d", len(gen.Tasks))
	}

	// 5. Store the SSH credential, then add the target machine using only
	// friendly fields (name, address, SSH user, credential, port).
	var cred store.CredentialMeta
	credBody := `{"name":"My SSH key","kind":"ssh_key","secret":"PRIVATE KEY MATERIAL"}`
	if code := postJSON(t, ts.URL+"/api/credentials", credBody, &cred); code != http.StatusCreated {
		t.Fatalf("store credential status = %d", code)
	}
	if cred.HasSecret == false || cred.ID == "" {
		t.Fatalf("unexpected credential: %+v", cred)
	}

	play := project.Playbooks[0].Plays[0]
	play.Tasks = gen.Tasks
	play.Handlers = gen.Handlers
	project.Inventories = []*ir.Inventory{{
		ID: "inv1", Name: "inventory",
		Groups: []*ir.InventoryGroup{{
			ID: "g1", Name: "web servers",
			Hosts: []*ir.Host{{
				ID: "h1", Name: "web01", Address: "10.0.0.11",
				SSHUser: "root", SSHPort: 22, CredentialID: cred.ID,
			}},
		}},
	}}
	projectBody, _ := json.Marshal(project)
	var saved ir.Project
	if code := putJSON(t, ts.URL+"/api/projects/"+project.ID, string(projectBody), &saved); code != http.StatusOK {
		t.Fatalf("save status = %d", code)
	}

	// 6. Test connection — plain-language result, no Ansible output required.
	var conn deploy.ConnectionResult
	hostBody := mustJSON(map[string]any{"host": map[string]any{
		"name": "web01", "address": "10.0.0.11", "sshUser": "root",
		"sshPort": 22, "credentialId": cred.ID,
	}})
	if code := postJSON(t, ts.URL+"/api/targets/test", hostBody, &conn); code != http.StatusOK {
		t.Fatalf("connection test status = %d", code)
	}
	if !conn.OK || conn.Message != "Connection works." {
		t.Fatalf("connection test failed: %+v", conn)
	}

	// 7. Review — steps must read as outcomes, not module names.
	var recognition actionRecognizeResponse
	if code := postJSON(t, ts.URL+"/api/actions/recognize",
		mustJSON(map[string]any{"tasks": gen.Tasks}), &recognition); code != http.StatusOK {
		t.Fatalf("recognize status = %d", code)
	}
	if len(recognition.Recognitions) != 2 {
		t.Fatalf("recognitions = %d", len(recognition.Recognitions))
	}
	labels := []string{recognition.Recognitions[0].Label, recognition.Recognitions[1].Label}
	want := []string{"Install nginx", "Start nginx"}
	for i, w := range want {
		if labels[i] != w {
			t.Errorf("review step %d = %q, want %q", i+1, labels[i], w)
		}
	}
	for _, rec := range recognition.Recognitions {
		if !rec.Recognized {
			t.Errorf("step %q would be shown as a technical task to a beginner", rec.TaskID)
		}
		// No Ansible jargon may leak into the primary label/subtitle.
		combined := rec.Label + " " + rec.Subtitle
		for _, jargon := range []string{"ansible.builtin", "state:", "present", "become"} {
			if strings.Contains(combined, jargon) {
				t.Errorf("beginner-facing copy contains %q: %q", jargon, combined)
			}
		}
	}

	// Preflight readiness is expressed per machine.
	var pre deploy.PreflightResult
	if code := postJSON(t, ts.URL+"/api/deployments/preflight",
		fmt.Sprintf(`{"projectId":%q,"inventoryId":"inv1"}`, project.ID), &pre); code != http.StatusOK {
		t.Fatalf("preflight status = %d", code)
	}
	if pre.Total != 1 || pre.Reachable != 1 || !pre.Hosts[0].OK {
		t.Fatalf("preflight = %+v", pre)
	}

	// 8. Deploy — runs and reports success without the user reading logs.
	var dep ir.Deployment
	if code := postJSON(t, ts.URL+"/api/deployments",
		fmt.Sprintf(`{"projectId":%q,"playbookId":%q,"inventoryId":"inv1"}`, project.ID, project.Playbooks[0].ID), &dep); code != http.StatusCreated {
		t.Fatalf("deploy status = %d", code)
	}
	deadline := 200
	for i := 0; i < deadline; i++ {
		var cur ir.Deployment
		getJSON(t, ts.URL+"/api/deployments/"+dep.ID, http.StatusOK, &cur)
		if cur.Status == ir.DeploymentSucceeded {
			break
		}
		if cur.Status == ir.DeploymentFailed {
			t.Fatalf("deployment failed: %+v", cur)
		}
		if i == deadline-1 {
			t.Fatal("deployment did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 9. The YAML escape hatch exists and is correct (advanced view only).
	var rendered renderResponse
	if code := postJSON(t, ts.URL+"/api/render", mustJSON(map[string]any{"playbook": saved.Playbooks[0]}), &rendered); code != http.StatusOK {
		t.Fatalf("render status = %d", code)
	}
	for _, wantYAML := range []string{"ansible.builtin.package:", "ansible.builtin.service:"} {
		if !strings.Contains(rendered.YAML, wantYAML) {
			t.Errorf("generated YAML missing %q:\n%s", wantYAML, rendered.YAML)
		}
	}
}

// TestUXAcceptanceExpertJourney covers the expert workflow that must not
// be sacrificed: browse all modules, add one directly, edit arbitrary
// arguments and task options, inspect the YAML, and import it back
// without losing anything.
func TestUXAcceptanceExpertJourney(t *testing.T) {
	runner := &fakeRunner{exitCode: 0, stdout: `ok`}
	ts := uxTestServer(t, runner)

	// Browse all installed modules (long tail).
	var modules moduleListResponse
	getJSON(t, ts.URL+"/api/modules", http.StatusOK, &modules)
	if len(modules.Modules) < 5 {
		t.Fatalf("modules = %d", len(modules.Modules))
	}

	// Inspect a module's real schema (dynamic form source).
	var schema ansible.ModuleSchema
	getJSON(t, ts.URL+"/api/modules/ansible.builtin.apt", http.StatusOK, &schema)
	if len(schema.Options) == 0 {
		t.Fatal("module schema empty")
	}

	// Add a module directly with arbitrary arguments and task options.
	project := &ir.Project{
		ID: "proj-expert", Name: "expert",
		Playbooks: []*ir.Playbook{{
			ID: "pb1", Name: "expert",
			Plays: []*ir.Play{{
				ID: "play1", Name: "expert play", Hosts: "all",
				Tasks: []*ir.Task{{
					ID: "t1", Name: "ufw rule", Module: "community.general.ufw",
					Args:     map[string]any{"rule": "allow", "port": "443", "proto": "tcp"},
					When:     "ansible_os_family == 'Debian'",
					Register: "ufw_result",
					Tags:     []string{"firewall"},
				}},
			}},
		}},
		Inventories: []*ir.Inventory{{
			ID: "inv1", Name: "inv",
			Hosts: []*ir.Host{{ID: "h1", Name: "node01", Address: "10.0.0.5"}},
		}},
	}
	body, _ := json.Marshal(project)
	var saved ir.Project
	if code := putJSON(t, ts.URL+"/api/projects/"+project.ID, string(body), &saved); code != http.StatusOK {
		t.Fatalf("expert save status = %d", code)
	}

	// The advanced task must stay Advanced (not silently simplified).
	var recognition actionRecognizeResponse
	postJSON(t, ts.URL+"/api/actions/recognize",
		mustJSON(map[string]any{"tasks": saved.Playbooks[0].Plays[0].Tasks}), &recognition)
	if recognition.Recognitions[0].Recognized {
		t.Fatal("task with when/register/tags must not be simplified")
	}

	// YAML round-trips with the advanced options intact.
	var rendered renderResponse
	if code := postJSON(t, ts.URL+"/api/render", mustJSON(map[string]any{"playbook": saved.Playbooks[0]}), &rendered); code != http.StatusOK {
		t.Fatalf("render status = %d", code)
	}
	for _, wantYAML := range []string{"community.general.ufw:", "when: ansible_os_family == 'Debian'", "register: ufw_result", "- firewall"} {
		if !strings.Contains(rendered.YAML, wantYAML) {
			t.Errorf("advanced YAML missing %q:\n%s", wantYAML, rendered.YAML)
		}
	}

	// And it can be imported back without losing data.
	var parsed parseResponse
	if code := postJSON(t, ts.URL+"/api/parse", mustJSON(map[string]any{"yaml": rendered.YAML}), &parsed); code != http.StatusOK {
		t.Fatalf("parse status = %d", code)
	}
	pb, _ := parsed.Playbook.(map[string]any)
	plays, _ := pb["plays"].([]any)
	if len(plays) != 1 {
		t.Fatalf("imported plays = %d", len(plays))
	}
	tasks, _ := plays[0].(map[string]any)["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("imported tasks = %d", len(tasks))
	}
	task, _ := tasks[0].(map[string]any)
	if task["when"] != "ansible_os_family == 'Debian'" || task["register"] != "ufw_result" {
		t.Fatalf("advanced options lost on import: %+v", task)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
