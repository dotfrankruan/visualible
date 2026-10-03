package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/parse"
	"github.com/dotfrankruan/visualible/internal/store"
)

// libraryServer builds a server with an in-memory store, matching what a
// Library user would have.
func libraryServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:", "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(fixtureDiscovery(t), st, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, st
}

func TestProjectListIncludesPlaybookSummaries(t *testing.T) {
	ts, st := libraryServer(t)
	ctx := context.Background()

	project := &ir.Project{
		ID: "p1", Name: "Homelab", Description: "home lab automation",
		Playbooks: []*ir.Playbook{
			{
				ID: "pb1", Name: "Bootstrap Debian",
				Plays: []*ir.Play{{ID: "pl1", Name: "bootstrap", Hosts: "all",
					Tasks: []*ir.Task{
						{ID: "t1", Name: "a", Module: "ansible.builtin.debug"},
						{ID: "t2", Name: "b", Module: "ansible.builtin.debug"},
					}}},
			},
			{
				ID: "pb2", Name: "Configure nginx",
				Plays: []*ir.Play{{ID: "pl2", Name: "nginx", Hosts: "web",
					Tasks: []*ir.Task{
						{ID: "t3", Name: "c", Module: "ansible.builtin.debug"},
					},
					Handlers: []*ir.Task{
						{ID: "h1", Name: "restart", Module: "ansible.builtin.debug"},
					}}},
			},
		},
		Inventories: []*ir.Inventory{},
	}
	if err := st.SaveProject(ctx, project); err != nil {
		t.Fatalf("save: %v", err)
	}

	var list projectListResponse
	getJSON(t, ts.URL+"/api/projects", http.StatusOK, &list)
	if len(list.Projects) != 1 {
		t.Fatalf("projects = %d", len(list.Projects))
	}
	meta := list.Projects[0]
	if meta.Name != "Homelab" || meta.Description != "home lab automation" {
		t.Fatalf("meta = %+v", meta)
	}
	if len(meta.Playbooks) != 2 {
		t.Fatalf("playbook summaries = %d", len(meta.Playbooks))
	}
	byID := map[string]store.PlaybookSummary{}
	for _, pb := range meta.Playbooks {
		byID[pb.ID] = pb
	}
	if byID["pb1"].Name != "Bootstrap Debian" || byID["pb1"].Steps != 2 {
		t.Errorf("bootstrap summary = %+v", byID["pb1"])
	}
	// Handlers count as steps, so the Library shows the real document size.
	if byID["pb2"].Steps != 2 {
		t.Errorf("nginx summary = %+v", byID["pb2"])
	}
	// A playbook saved without explicit metadata still reports a time.
	if byID["pb1"].UpdatedAt.IsZero() {
		t.Error("playbook summaries need a modified time for the Library")
	}
}

func TestPlaybookExportEndpoint(t *testing.T) {
	ts, st := libraryServer(t)
	ctx := context.Background()
	project := &ir.Project{
		ID: "p1", Name: "Homelab",
		Playbooks: []*ir.Playbook{{
			ID: "pb1", Name: "Configure nginx",
			Plays: []*ir.Play{{ID: "pl1", Name: "nginx", Hosts: "web",
				Tasks: []*ir.Task{
					{ID: "t1", Name: "Install nginx", Module: "ansible.builtin.package",
						Args: map[string]any{"name": "nginx", "state": "present"}},
				}}},
		}},
		Inventories: []*ir.Inventory{{
			ID: "inv1", Name: "inventory",
			Hosts: []*ir.Host{{ID: "h1", Name: "web01", CredentialID: "cred-1"}},
		}},
	}
	if err := st.SaveProject(ctx, project); err != nil {
		t.Fatalf("save: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/projects/p1/playbooks/pb1/export/yaml")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "configure-nginx.yml") {
		t.Fatalf("content-disposition = %q", cd)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Fatalf("content-type = %q", ct)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	yaml := string(buf[:n])
	if !strings.Contains(yaml, "ansible.builtin.package:") {
		t.Fatalf("export body wrong:\n%s", yaml)
	}
	// Exports are standard Ansible content: no credentials, no Visualible
	// bookkeeping. The project references a credential by ID only.
	for _, leak := range []string{"cred-1", "visualible", "credentialId"} {
		if strings.Contains(strings.ToLower(yaml), strings.ToLower(leak)) {
			t.Errorf("export leaked %q:\n%s", leak, yaml)
		}
	}
}

func TestPlaybookExportUnknownPlaybook(t *testing.T) {
	ts, st := libraryServer(t)
	ctx := context.Background()
	project := &ir.Project{
		ID: "p1", Name: "Homelab",
		Playbooks: []*ir.Playbook{{ID: "pb1", Name: "One",
			Plays: []*ir.Play{{ID: "pl1", Name: "x", Hosts: "all"}}}},
		Inventories: []*ir.Inventory{},
	}
	if err := st.SaveProject(ctx, project); err != nil {
		t.Fatalf("save: %v", err)
	}
	var er errorResponse
	getJSON(t, ts.URL+"/api/projects/p1/playbooks/nope/export/yaml", http.StatusNotFound, &er)
	if er.Error.Code != "playbook_not_found" {
		t.Fatalf("code = %q", er.Error.Code)
	}
	var er2 errorResponse
	getJSON(t, ts.URL+"/api/projects/p1/playbooks/pb1/export/docx", http.StatusBadRequest, &er2)
	if er2.Error.Code != "unsupported_format" {
		t.Fatalf("code = %q", er2.Error.Code)
	}
}

func TestUniqueExportFilenamesWithinProject(t *testing.T) {
	project := &ir.Project{
		ID: "p1", Name: "Homelab",
		Playbooks: []*ir.Playbook{
			{ID: "pb1", Name: "Configure nginx"},
			{ID: "pb2", Name: "Configure nginx"},
		},
	}
	got := uniqueInProject(project, "configure-nginx.yml", "pb2")
	if got != "configure-nginx-2.yml" {
		t.Fatalf("duplicate playbook names must not overwrite exports: %q", got)
	}
	// The exported document itself is not counted as a conflict.
	got = uniqueInProject(project, "configure-nginx.yml", "pb1")
	if got != "configure-nginx-2.yml" {
		t.Fatalf("got %q", got)
	}
}

// TestLibraryAcceptanceScenario walks the phase's acceptance journey over
// HTTP: create a project, add two playbooks, reopen, and export one as
// standard Ansible YAML.
func TestLibraryAcceptanceScenario(t *testing.T) {
	ts, st := libraryServer(t)
	ctx := context.Background()

	// Library -> create project "Homelab".
	var project ir.Project
	if code := postJSON(t, ts.URL+"/api/projects", `{"name":"Homelab"}`, &project); code != http.StatusCreated {
		t.Fatalf("create project status = %d", code)
	}
	if len(project.Playbooks) != 1 {
		t.Fatalf("a new project should start with one playbook: %+v", project.Playbooks)
	}

	// Create playbook "Bootstrap Debian" and "Configure nginx".
	bootstrap := &ir.Playbook{
		ID: "pb-bootstrap", Name: "Bootstrap Debian",
		Plays: []*ir.Play{{ID: "pl1", Name: "bootstrap", Hosts: "all",
			Tasks: []*ir.Task{
				{ID: "t1", Name: "Install curl", Module: "ansible.builtin.package",
					Args: map[string]any{"name": "curl", "state": "present"}},
			}}},
	}
	nginx := &ir.Playbook{
		ID: "pb-nginx", Name: "Configure nginx",
		Plays: []*ir.Play{{ID: "pl2", Name: "nginx", Hosts: "web",
			Tasks: []*ir.Task{
				{ID: "t2", Name: "Install nginx", Module: "ansible.builtin.package",
					Args: map[string]any{"name": "nginx", "state": "present"}},
			}}},
	}
	project.Playbooks = append(project.Playbooks, bootstrap, nginx)
	body := mustJSON(project)
	var saved ir.Project
	if code := putJSON(t, ts.URL+"/api/projects/"+project.ID, body, &saved); code != http.StatusOK {
		t.Fatalf("save status = %d", code)
	}
	if len(saved.Playbooks) != 3 {
		t.Fatalf("playbooks after save = %d", len(saved.Playbooks))
	}

	// Validate with the store directly that both documents persisted.
	stored, err := st.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(stored.Playbooks) != 3 {
		t.Fatalf("reopened playbooks = %d", len(stored.Playbooks))
	}
	names := map[string]bool{}
	for _, pb := range stored.Playbooks {
		names[pb.Name] = true
	}
	for _, want := range []string{"Bootstrap Debian", "Configure nginx"} {
		if !names[want] {
			t.Errorf("playbook %q lost on reopen", want)
		}
	}

	// Library listing shows both documents without loading them.
	var list projectListResponse
	getJSON(t, ts.URL+"/api/projects", http.StatusOK, &list)
	var found *store.ProjectMeta
	for i := range list.Projects {
		if list.Projects[i].ID == project.ID {
			found = &list.Projects[i]
		}
	}
	if found == nil {
		t.Fatal("project missing from the library listing")
	}
	if len(found.Playbooks) != 3 {
		t.Fatalf("library summaries = %d", len(found.Playbooks))
	}

	// Export "Configure nginx" as YAML.
	resp, err := http.Get(ts.URL + "/api/projects/" + project.ID + "/playbooks/pb-nginx/export/yaml")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "configure-nginx.yml") {
		t.Fatalf("filename = %q", cd)
	}
	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	exported := string(buf[:n])

	// The export must be usable outside Visualible: only this playbook, and
	// parseable as ordinary Ansible YAML.
	if strings.Contains(exported, "curl") {
		t.Errorf("export included another playbook:\n%s", exported)
	}
	if !strings.Contains(exported, "hosts: web") {
		t.Errorf("export missing the playbook's own content:\n%s", exported)
	}
	parsed, err := parse.Playbook("configure-nginx", []byte(exported))
	if err != nil {
		t.Fatalf("exported YAML is not valid Ansible YAML: %v", err)
	}
	if parsed.HasErrors() || len(parsed.Playbook.Plays[0].Tasks) != 1 {
		t.Fatalf("exported YAML does not round-trip: %+v", parsed.Diagnostics)
	}
}

// TestSPAFallbackServesDeepLinks: /projects/{id}/playbooks/{pid} is a
// client-side route, so a refresh must return the app shell (which then
// restores the view from the URL) instead of a 404.
func TestSPAFallbackServesDeepLinks(t *testing.T) {
	ts, _ := libraryServer(t)
	for _, path := range []string{"/", "/library", "/projects/abc", "/projects/abc/playbooks/xyz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(string(body[:n]), "Visualible") {
			t.Errorf("GET %s did not serve the app shell: %q", path, body[:n])
		}
	}
	// Missing assets must still be a real 404, not the HTML shell.
	resp, err := http.Get(ts.URL + "/js/does-not-exist.js")
	if err != nil {
		t.Fatalf("GET asset: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want 404", resp.StatusCode)
	}
}
