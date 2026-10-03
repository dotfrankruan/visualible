package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubProvider struct {
	response string
	err      error
	gotMsgs  []Message
}

func (s *stubProvider) Complete(ctx context.Context, msgs []Message) (string, error) {
	s.gotMsgs = msgs
	return s.response, s.err
}

func TestExtractYAML(t *testing.T) {
	cases := map[string]string{
		"- name: x\n  hosts: all\n":                                 "- name: x\n  hosts: all",
		"```yaml\n- name: x\n  hosts: all\n```":                     "- name: x\n  hosts: all",
		"```\n- name: x\n  hosts: all\n```":                         "- name: x\n  hosts: all",
		"Here you go:\n```yaml\n- name: x\n  hosts: all\n```\nDone": "- name: x\n  hosts: all",
		"Some prose\n- name: x\n  hosts: all\ntrailing":             "- name: x\n  hosts: all\ntrailing",
	}
	for in, want := range cases {
		if got := ExtractYAML(in); got != want {
			t.Errorf("ExtractYAML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneratePlaybook(t *testing.T) {
	p := &stubProvider{response: "```yaml\n- name: web\n  hosts: all\n  tasks:\n    - name: Install nginx\n      ansible.builtin.apt:\n        name: nginx\n        state: present\n```"}
	res, raw, err := GeneratePlaybook(context.Background(), p, "install nginx", "")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(raw, "```yaml") {
		t.Fatalf("raw not preserved: %q", raw)
	}
	if res.HasErrors() {
		t.Fatalf("unexpected diagnostics: %+v", res.Diagnostics)
	}
	pb := res.Playbook
	if len(pb.Plays) != 1 || len(pb.Plays[0].Tasks) != 1 {
		t.Fatalf("playbook wrong: %+v", pb)
	}
	if pb.Plays[0].Tasks[0].Module != "ansible.builtin.apt" {
		t.Fatalf("module wrong: %+v", pb.Plays[0].Tasks[0])
	}
	if err := pb.Validate(); err != nil {
		t.Fatalf("generated playbook invalid: %v", err)
	}
	// Prompt must include the intent and the rules.
	if len(p.gotMsgs) != 2 || !strings.Contains(p.gotMsgs[1].Content, "install nginx") {
		t.Fatalf("prompt wrong: %+v", p.gotMsgs)
	}
}

func TestGeneratePlaybookBadOutput(t *testing.T) {
	p := &stubProvider{response: "I cannot help with that."}
	_, _, err := GeneratePlaybook(context.Background(), p, "x", "")
	if err == nil {
		t.Fatal("expected parse failure for non-playbook output")
	}
}

func TestGeneratePlaybookEmptyIntent(t *testing.T) {
	if _, _, err := GeneratePlaybook(context.Background(), &stubProvider{}, "  ", ""); err == nil {
		t.Fatal("expected empty-intent error")
	}
}

func TestOpenAIProvider(t *testing.T) {
	var gotAuth, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = string(buf)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "- name: x\n  hosts: all"}},
			},
		})
	}))
	defer ts.Close()

	p := NewOpenAIProvider(Config{
		Endpoint: ts.URL + "/v1",
		Model:    "test-model",
		APIKey:   "secret-key",
		Headers:  map[string]string{"X-Custom": "yes"},
	})
	out, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(out, "hosts: all") {
		t.Fatalf("output = %q", out)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotBody, "test-model") {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestOpenAIProviderError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "bad key"},
		})
	}))
	defer ts.Close()
	p := NewOpenAIProvider(Config{Endpoint: ts.URL, Model: "m"})
	_, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{}).Validate(); err == nil {
		t.Fatal("expected error for empty config")
	}
	if err := (Config{Endpoint: "notaurl", Model: "m"}).Validate(); err == nil {
		t.Fatal("expected error for bad URL")
	}
	if err := (Config{Endpoint: "http://localhost:11434/v1", Model: "m"}).Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
