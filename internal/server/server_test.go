package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/visualible/visualible/internal/ansible"
	"github.com/visualible/visualible/internal/store"
)

func fixtureDiscovery(t *testing.T) *ansible.Discovery {
	t.Helper()
	cache, err := ansible.OpenCache(t.TempDir())
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	doc := ansible.NewDocClientWith("/fake/ansible-doc",
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if args[0] == "-l" {
				return os.ReadFile("testdata/doc-list.json")
			}
			return os.ReadFile("testdata/doc-apt.json")
		})
	return ansible.NewDiscoveryWith(&ansible.Installation{Path: "/fake", Version: "2.16.3"}, doc, cache)
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := New(fixtureDiscovery(t), st)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func getJSON(t *testing.T, url string, wantStatus int, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d, want %d", url, resp.StatusCode, wantStatus)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	var h healthResponse
	getJSON(t, ts.URL+"/api/health", http.StatusOK, &h)
	if !h.Ansible.Available || h.Ansible.Version != "2.16.3" {
		t.Fatalf("unexpected health: %+v", h)
	}
}

func TestModuleList(t *testing.T) {
	ts := newTestServer(t)
	var ml moduleListResponse
	getJSON(t, ts.URL+"/api/modules", http.StatusOK, &ml)
	if len(ml.Modules) != 5 {
		t.Fatalf("modules = %d", len(ml.Modules))
	}
	if ml.Modules[0].FQCN != "ansible.builtin.apt" {
		t.Fatalf("first = %q", ml.Modules[0].FQCN)
	}
}

func TestModuleDoc(t *testing.T) {
	ts := newTestServer(t)
	var schema ansible.ModuleSchema
	getJSON(t, ts.URL+"/api/modules/ansible.builtin.apt", http.StatusOK, &schema)
	if schema.Name != "apt" || len(schema.Options) != 9 {
		t.Fatalf("unexpected schema: %+v", schema)
	}
}

func TestModuleDocInvalidFQCN(t *testing.T) {
	ts := newTestServer(t)
	var er errorResponse
	getJSON(t, ts.URL+"/api/modules/notamodule", http.StatusBadRequest, &er)
	if er.Error.Code != "invalid_fqcn" {
		t.Fatalf("code = %q", er.Error.Code)
	}
}

func TestRenderEndpoint(t *testing.T) {
	ts := newTestServer(t)
	body := `{"playbook":{"id":"pb1","name":"demo","plays":[{"id":"p1","name":"Configure webserver","hosts":"web","tasks":[{"id":"t1","name":"Install nginx","module":"ansible.builtin.apt","args":{"name":"nginx","state":"present"}}]}]}}`
	resp, err := http.Post(ts.URL+"/api/render", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var rr renderResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(rr.YAML, "ansible.builtin.apt:") || !strings.Contains(rr.YAML, "hosts: web") {
		t.Fatalf("unexpected yaml:\n%s", rr.YAML)
	}
}

func TestRenderEndpointValidationError(t *testing.T) {
	ts := newTestServer(t)
	body := `{"playbook":{"id":"pb1","name":"demo","plays":[{"id":"p1","name":"x","hosts":"","tasks":[]}]}}`
	resp, err := http.Post(ts.URL+"/api/render", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var er errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if er.Error.Code != "invalid_playbook" || len(er.Error.Problems) == 0 {
		t.Fatalf("unexpected error: %+v", er)
	}
}

func TestFrontendServed(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "Visualible") {
		t.Fatalf("frontend not served: %q", buf[:n])
	}
}

func TestAnsibleUnavailable(t *testing.T) {
	cache, err := ansible.OpenCache(t.TempDir())
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	srv, err := New(ansible.NewDiscoveryWith(nil, nil, cache), nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	var er errorResponse
	getJSON(t, ts.URL+"/api/modules", http.StatusServiceUnavailable, &er)
	if er.Error.Code != "ansible_unavailable" {
		t.Fatalf("code = %q", er.Error.Code)
	}
}
