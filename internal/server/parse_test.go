package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestParseEndpoint(t *testing.T) {
	ts := newTestServer(t)
	body := `{"yaml":"- name: Configure webserver\n  hosts: web\n  tasks:\n    - name: Install nginx\n      ansible.builtin.apt:\n        name: nginx\n        state: present\n"}`
	resp, err := http.Post(ts.URL+"/api/parse", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var pr parseResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pb, ok := pr.Playbook.(map[string]any)
	if !ok {
		t.Fatalf("playbook missing: %+v", pr)
	}
	plays, _ := pb["plays"].([]any)
	if len(plays) != 1 {
		t.Fatalf("plays = %d", len(plays))
	}
}

func TestParseEndpointInvalid(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/parse", "application/json",
		strings.NewReader(`{"yaml":"not: a playbook"}`))
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
	if er.Error.Code != "parse_failed" {
		t.Fatalf("code = %q", er.Error.Code)
	}
}
