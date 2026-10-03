package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
)

func TestProjectLifecycle(t *testing.T) {
	ts := newTestServer(t)

	// Create.
	resp, err := http.Post(ts.URL+"/api/projects", "application/json",
		bytes.NewReader([]byte(`{"name":"homelab","description":"test lab"}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var created ir.Project
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	if created.ID == "" || len(created.Playbooks) != 1 {
		t.Fatalf("unexpected created project: %+v", created)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("createdAt not set")
	}

	// List.
	var list projectListResponse
	getJSON(t, ts.URL+"/api/projects", http.StatusOK, &list)
	if len(list.Projects) != 1 || list.Projects[0].Name != "homelab" {
		t.Fatalf("unexpected list: %+v", list)
	}
	if list.Projects[0].Description != "test lab" {
		t.Fatalf("description = %q", list.Projects[0].Description)
	}

	// Get.
	var got ir.Project
	getJSON(t, ts.URL+"/api/projects/"+created.ID, http.StatusOK, &got)
	if got.Name != "homelab" {
		t.Fatalf("name = %q", got.Name)
	}

	// Update: add a task to the play.
	got.Playbooks[0].Plays[0].Tasks = append(got.Playbooks[0].Plays[0].Tasks, &ir.Task{
		ID: "t1", Name: "Install nginx", Module: "ansible.builtin.apt",
		Args: map[string]any{"name": "nginx"},
	})
	body, _ := json.Marshal(got)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/projects/"+got.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	uresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	uresp.Body.Close()
	if uresp.StatusCode != http.StatusOK {
		t.Fatalf("put status = %d", uresp.StatusCode)
	}

	var reloaded ir.Project
	getJSON(t, ts.URL+"/api/projects/"+created.ID, http.StatusOK, &reloaded)
	if len(reloaded.Playbooks[0].Plays[0].Tasks) != 1 {
		t.Fatalf("task not persisted: %+v", reloaded.Playbooks[0].Plays[0])
	}

	// Delete.
	dreq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/projects/"+created.ID, nil)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", dresp.StatusCode)
	}
	var er errorResponse
	getJSON(t, ts.URL+"/api/projects/"+created.ID, http.StatusNotFound, &er)
}

func TestProjectPutValidation(t *testing.T) {
	ts := newTestServer(t)
	body := `{"id":"x","name":"bad","playbooks":[{"id":"pb","name":"b","plays":[{"id":"p","name":"x","hosts":"","tasks":[]}]}],"inventories":[]}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/projects/x", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var er errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if er.Error.Code != "invalid_project" || len(er.Error.Problems) == 0 {
		t.Fatalf("unexpected error: %+v", er)
	}
}

func TestProjectPutIDMismatch(t *testing.T) {
	ts := newTestServer(t)
	body := `{"id":"y","name":"x","playbooks":[],"inventories":[]}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/projects/x", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
