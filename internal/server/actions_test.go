package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/action"
	"github.com/dotfrankruan/visualible/internal/ansible"
)

func TestActionList(t *testing.T) {
	ts := newTestServer(t)
	var res actionListResponse
	getJSON(t, ts.URL+"/api/actions", http.StatusOK, &res)
	if len(res.Actions) < 8 {
		t.Fatalf("actions = %d", len(res.Actions))
	}
	foundDocker := false
	for _, a := range res.Actions {
		if a.ID == "docker-container" {
			foundDocker = true
			// Fixture module list includes community.docker, so the Action
			// must be available here.
			if !a.Available {
				t.Fatalf("docker action should be available: %+v", a)
			}
		}
	}
	if !foundDocker {
		t.Fatal("docker action missing")
	}
}

func TestActionAvailabilityGatedOnCollection(t *testing.T) {
	// Discovery whose catalog lacks community.docker.
	cache, err := ansible.OpenCache(t.TempDir())
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	doc := ansible.NewDocClientWith("/fake/ansible-doc",
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte(`{"ansible.builtin.apt":"Manage apt packages"}`), nil
		})
	srv, err := New(ansible.NewDiscoveryWith(&ansible.Installation{Path: "/fake"}, doc, cache), nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	var res actionListResponse
	getJSON(t, ts.URL+"/api/actions", http.StatusOK, &res)
	for _, a := range res.Actions {
		if a.ID != "docker-container" {
			continue
		}
		if a.Available {
			t.Fatalf("docker action should be unavailable: %+v", a)
		}
		if !strings.Contains(a.Reason, "community.docker") {
			t.Fatalf("reason = %q", a.Reason)
		}
	}
}

func TestActionGenerateEndpoint(t *testing.T) {
	ts := newTestServer(t)
	body := `{"action":"install-software","params":{"package":"nginx","state":"present","serviceName":"nginx","ensureRunning":true,"enableBoot":true}}`
	resp, err := http.Post(ts.URL+"/api/actions/generate", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var gen action.Generated
	if err := json.NewDecoder(resp.Body).Decode(&gen); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(gen.Tasks) != 2 {
		t.Fatalf("compound generation lost: %d tasks", len(gen.Tasks))
	}
	if gen.Tasks[0].Module != "ansible.builtin.package" || gen.Tasks[1].Module != "ansible.builtin.service" {
		t.Fatalf("modules = %s / %s", gen.Tasks[0].Module, gen.Tasks[1].Module)
	}
}

func TestActionGenerateValidationError(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/actions/generate", "application/json",
		bytes.NewReader([]byte(`{"action":"install-software","params":{}}`)))
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
	if er.Error.Code != "action_failed" || !strings.Contains(er.Error.Message, "Package") {
		t.Fatalf("unexpected error: %+v", er)
	}
}

func TestActionRecognizeEndpoint(t *testing.T) {
	ts := newTestServer(t)
	body := `{"tasks":[
		{"id":"t1","name":"Install nginx","module":"ansible.builtin.package","args":{"name":"nginx","state":"present"}},
		{"id":"t2","name":"custom","module":"ansible.builtin.debug","args":{"msg":"hi"},"when":"x == 1"}
	]}`
	resp, err := http.Post(ts.URL+"/api/actions/recognize", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var res actionRecognizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Recognitions) != 2 {
		t.Fatalf("recognitions = %d", len(res.Recognitions))
	}
	first := res.Recognitions[0]
	if !first.Recognized || first.Action != "install-software" || first.Label != "Install nginx" {
		t.Fatalf("first = %+v", first)
	}
	if res.Recognitions[1].Recognized {
		t.Fatalf("advanced task should not be recognized: %+v", res.Recognitions[1])
	}
}

// No-loss check across the Simple/Advanced boundary: recognize a
// generated task, regenerate from the curated params, and confirm the
// task is unchanged.
func TestActionRoundTripThroughAPI(t *testing.T) {
	ts := newTestServer(t)
	genBody := `{"action":"render-template","params":{"templatePath":"nginx.conf.j2","dest":"/etc/nginx/nginx.conf","restartService":"nginx"}}`
	resp, err := http.Post(ts.URL+"/api/actions/generate", "application/json", strings.NewReader(genBody))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var gen action.Generated
	json.NewDecoder(resp.Body).Decode(&gen)
	resp.Body.Close()

	tasksJSON, _ := json.Marshal(map[string]any{"tasks": gen.Tasks})
	rresp, err := http.Post(ts.URL+"/api/actions/recognize", "application/json", bytes.NewReader(tasksJSON))
	if err != nil {
		t.Fatalf("recognize: %v", err)
	}
	var rec actionRecognizeResponse
	json.NewDecoder(rresp.Body).Decode(&rec)
	rresp.Body.Close()
	if !rec.Recognitions[0].Recognized {
		t.Fatalf("generated task not recognized: %+v", rec.Recognitions[0])
	}

	paramsJSON, _ := json.Marshal(map[string]any{"action": rec.Recognitions[0].Action, "params": rec.Recognitions[0].Params})
	gresp, err := http.Post(ts.URL+"/api/actions/generate", "application/json", bytes.NewReader(paramsJSON))
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	var gen2 action.Generated
	json.NewDecoder(gresp.Body).Decode(&gen2)
	gresp.Body.Close()

	if gen2.Tasks[0].Module != gen.Tasks[0].Module {
		t.Fatalf("module drifted: %s -> %s", gen.Tasks[0].Module, gen2.Tasks[0].Module)
	}
	for k, v := range gen.Tasks[0].Args {
		if gen2.Tasks[0].Args[k] != v {
			t.Fatalf("arg %s drifted: %v -> %v", k, v, gen2.Tasks[0].Args[k])
		}
	}
	if len(gen2.Handlers) != 1 || gen2.Handlers[0].Name != "Restart nginx" {
		t.Fatalf("handler lost in round trip: %+v", gen2.Handlers)
	}
}
