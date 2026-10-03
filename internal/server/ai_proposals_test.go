package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/store"
)

// stubOpenAI serves one canned chat completion.
func stubOpenAI(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": response}},
			},
		})
	}))
}

func configureAI(t *testing.T, st *store.Store, endpoint string) {
	t.Helper()
	settings, err := st.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	settings.AI.Endpoint = endpoint
	settings.AI.Model = "test-model"
	if err := st.SaveSettings(context.Background(), settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
}

const proposalIR = `{"name":"demo","plays":[{"id":"play1","name":"web","hosts":"web","become":true,"tasks":[
{"id":"t1","name":"Install nginx","module":"ansible.builtin.package","args":{"name":"nginx","state":"present"}},
{"id":"t2","name":"Start nginx","module":"ansible.builtin.service","args":{"name":"nginx","state":"started","enabled":true},"become":true}
]}]}`

func newAITestServer(t *testing.T, aiResponse string) (*httptest.Server, *httptest.Server) {
	t.Helper()
	aiSrv := stubOpenAI(t, aiResponse)
	t.Cleanup(aiSrv.Close)

	st, err := store.Open(":memory:", "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	configureAI(t, st, aiSrv.URL+"/v1")

	srv, err := New(fixtureDiscovery(t), st, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, aiSrv
}

func TestAIProposalFlow(t *testing.T) {
	ts, _ := newAITestServer(t, proposalIR)

	// Create a proposal against an empty base.
	body := `{"intent":"install nginx","baseRevision":1}`
	resp, err := http.Post(ts.URL+"/api/ai/proposals", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var p Proposal
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Status != ProposalReady {
		t.Fatalf("status = %s, diagnostics %v", p.Status, p.Diagnostics)
	}
	if p.ProposedIR == nil || len(p.ProposedIR.Plays[0].Tasks) != 2 {
		t.Fatalf("proposed IR wrong: %+v", p.ProposedIR)
	}
	if p.Diff == nil || p.Diff.Summary.Added != 2 {
		t.Fatalf("diff wrong: %+v", p.Diff)
	}
	if p.Model != "test-model" {
		t.Fatalf("model = %q", p.Model)
	}
	if p.BaseHash == "" {
		t.Fatal("base hash missing")
	}

	// Merge: accept only the first addition.
	var addID string
	for _, c := range p.Diff.Changes {
		if c.Kind == "added" && c.TaskID == "t1" {
			addID = c.ID
		}
	}
	mergeBody, _ := json.Marshal(aiMergeRequest{
		Base:     &ir.Playbook{ID: "pb", Name: "demo", Plays: []*ir.Play{{ID: "play1", Name: "web", Hosts: "web", Tasks: []*ir.Task{}}}},
		Proposed: p.ProposedIR,
		Accepted: []string{addID},
	})
	mresp, err := http.Post(ts.URL+"/api/ai/merge", "application/json", bytes.NewReader(mergeBody))
	if err != nil {
		t.Fatalf("merge POST: %v", err)
	}
	defer mresp.Body.Close()
	if mresp.StatusCode != http.StatusOK {
		t.Fatalf("merge status = %d", mresp.StatusCode)
	}
	var mr aiMergeResponse
	if err := json.NewDecoder(mresp.Body).Decode(&mr); err != nil {
		t.Fatalf("decode merge: %v", err)
	}
	if len(mr.Playbook.Plays[0].Tasks) != 1 || mr.Playbook.Plays[0].Tasks[0].ID != "t1" {
		t.Fatalf("merged tasks wrong: %+v", mr.Playbook.Plays[0].Tasks)
	}
}

func TestAIProposalFailedValidation(t *testing.T) {
	// Model returns a play with no hosts and a task with no module.
	bad := `{"plays":[{"id":"p","name":"x","hosts":"","tasks":[{"id":"t1","name":"x"}]}]}`
	ts, _ := newAITestServer(t, bad)
	resp, err := http.Post(ts.URL+"/api/ai/proposals", "application/json",
		strings.NewReader(`{"intent":"break things","baseRevision":1}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var p Proposal
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Status != ProposalFailed {
		t.Fatalf("status = %s", p.Status)
	}
	if len(p.Diagnostics) == 0 {
		t.Fatal("diagnostics missing")
	}
}

func TestAIProposalNotConfigured(t *testing.T) {
	st, err := store.Open(":memory:", "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	srv, err := New(fixtureDiscovery(t), st, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/ai/proposals", "application/json",
		strings.NewReader(`{"intent":"x","baseRevision":1}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAIMergeRejectsInvalid(t *testing.T) {
	ts := newTestServer(t)
	// Base with notify->handler; proposed drops the handler. Accepting
	// the removal must be blocked.
	base := `{"id":"pb","name":"x","plays":[{"id":"p1","name":"x","hosts":"all","tasks":[{"id":"t1","name":"a","module":"ansible.builtin.template","notify":["Restart nginx"]}],"handlers":[{"id":"h1","name":"Restart nginx","module":"ansible.builtin.service"}]}]}`
	proposed := `{"id":"pb","name":"x","plays":[{"id":"p1","name":"x","hosts":"all","tasks":[{"id":"t1","name":"a","module":"ansible.builtin.template","notify":["Restart nginx"]}],"handlers":[]}]}`
	mergeBody := `{"base":` + base + `,"proposed":` + proposed + `,"accepted":["handlers:removed:h1"]}`
	resp, err := http.Post(ts.URL+"/api/ai/merge", "application/json", strings.NewReader(mergeBody))
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
	if er.Error.Code != "merge_invalid" {
		t.Fatalf("code = %q", er.Error.Code)
	}
}
