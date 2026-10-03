package ai

import (
	"context"
	"strings"
	"testing"
)

type captureProvider struct {
	response string
	err      error
	got      []Message
}

func (c *captureProvider) Complete(ctx context.Context, msgs []Message) (string, error) {
	c.got = msgs
	return c.response, c.err
}

const validIRJSON = `{"name":"demo","plays":[{"id":"play1","name":"web","hosts":"web","become":true,"tasks":[
{"id":"t1","name":"Install nginx","module":"ansible.builtin.package","args":{"name":"nginx","state":"present"}},
{"id":"t2","name":"Start nginx","module":"ansible.builtin.service","args":{"name":"nginx","state":"started","enabled":true},"become":true}
]}]}`

func TestProposeIR(t *testing.T) {
	p := &captureProvider{response: validIRJSON}
	prop, err := ProposeIR(context.Background(), p, "install nginx", nil)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	pb := prop.Playbook
	if len(pb.Plays) != 1 || len(pb.Plays[0].Tasks) != 2 {
		t.Fatalf("plays wrong: %+v", pb)
	}
	if pb.Plays[0].Tasks[0].ID != "t1" {
		t.Fatalf("existing id not preserved: %+v", pb.Plays[0].Tasks[0])
	}
	if err := pb.Validate(); err != nil {
		t.Fatalf("proposed IR invalid: %v", err)
	}
	// Intent must reach the model; system prompt must carry the contract.
	if !strings.Contains(p.got[1].Content, "install nginx") {
		t.Fatalf("intent missing from prompt: %+v", p.got[1])
	}
	if !strings.Contains(p.got[0].Content, "COMPLETE resulting automation") {
		t.Fatalf("system prompt missing contract")
	}
}

func TestProposeIRIncludesCurrentContext(t *testing.T) {
	p := &captureProvider{response: validIRJSON}
	current, err := ParseIR(validIRJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = ProposeIR(context.Background(), p, "change port", current)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if !strings.Contains(p.got[1].Content, "Current automation") ||
		!strings.Contains(p.got[1].Content, `"t1"`) {
		t.Fatalf("current IR not included: %s", p.got[1].Content)
	}
}

func TestProposeIRToleratesFences(t *testing.T) {
	p := &captureProvider{response: "Here is the result:\n```json\n" + validIRJSON + "\n```\nDone."}
	prop, err := ProposeIR(context.Background(), p, "x", nil)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(prop.Playbook.Plays) != 1 {
		t.Fatalf("plays = %d", len(prop.Playbook.Plays))
	}
}

func TestParseIRAssignsMissingIDs(t *testing.T) {
	raw := `{"plays":[{"hosts":"all","tasks":[{"name":"x","module":"ansible.builtin.debug"}]}]}`
	pb, err := ParseIR(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pb.ID == "" || pb.Plays[0].ID == "" || pb.Plays[0].Tasks[0].ID == "" {
		t.Fatalf("ids not assigned: %+v", pb)
	}
	if pb.Plays[0].Hosts != "all" {
		t.Fatalf("hosts = %q", pb.Plays[0].Hosts)
	}
}

func TestParseIRDeduplicatesIDs(t *testing.T) {
	raw := `{"plays":[{"id":"p","hosts":"all","tasks":[
		{"id":"dup","name":"a","module":"ansible.builtin.debug"},
		{"id":"dup","name":"b","module":"ansible.builtin.debug"}]}]}`
	pb, err := ParseIR(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	t0, t1 := pb.Plays[0].Tasks[0], pb.Plays[0].Tasks[1]
	if t0.ID == t1.ID {
		t.Fatalf("duplicate ids not fixed: %s", t0.ID)
	}
}

func TestParseIRRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"no json here", `{"plays":[]}`, `[1,2,3]`, ""} {
		if _, err := ParseIR(raw); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}

func TestProposeIREnvelopeWithRationale(t *testing.T) {
	raw := `{"playbook":` + validIRJSON + `,"rationale":["Kept nginx installed.","Added service start.","  ",""]}`
	prop, err := ParseProposal(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prop.Playbook.Plays) != 1 {
		t.Fatalf("playbook missing from envelope: %+v", prop.Playbook)
	}
	if len(prop.Rationale) != 2 {
		t.Fatalf("rationale = %v (blanks must be dropped)", prop.Rationale)
	}
	if prop.Rationale[0] != "Kept nginx installed." {
		t.Fatalf("rationale = %v", prop.Rationale)
	}
}

func TestParseProposalBarePlaybookHasNoRationale(t *testing.T) {
	prop, err := ParseProposal(validIRJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if prop.Rationale != nil {
		t.Fatalf("unexpected rationale: %v", prop.Rationale)
	}
}

func TestExtractJSONBalanced(t *testing.T) {
	raw := `prefix {"a": {"b": "brace} inside"}, "c": [1]} suffix`
	got := extractJSON(raw)
	if !strings.HasPrefix(got, `{"a"`) || !strings.HasSuffix(got, `]}`) {
		t.Fatalf("extract wrong: %q", got)
	}
}
