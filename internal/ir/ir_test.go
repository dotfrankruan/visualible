package ir

import (
	"strings"
	"testing"
)

func validPlaybook() *Playbook {
	return &Playbook{
		ID:   "pb1",
		Name: "webserver",
		Plays: []*Play{{
			ID:    "play1",
			Name:  "Configure webserver",
			Hosts: "web",
			Tasks: []*Task{
				{ID: "t1", Name: "Install nginx", Module: "ansible.builtin.apt",
					Args: map[string]any{"name": "nginx", "state": "present"}},
			},
			Handlers: []*Task{
				{ID: "h1", Name: "Restart nginx", Module: "ansible.builtin.systemd_service"},
			},
		}},
	}
}

func TestPlaybookValidateOK(t *testing.T) {
	if err := validPlaybook().Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateRequiresHosts(t *testing.T) {
	pb := validPlaybook()
	pb.Plays[0].Hosts = " "
	err := pb.Validate()
	if err == nil || !strings.Contains(err.Error(), "hosts is required") {
		t.Fatalf("expected hosts error, got %v", err)
	}
}

func TestValidateRequiresModule(t *testing.T) {
	pb := validPlaybook()
	pb.Plays[0].Tasks[0].Module = ""
	err := pb.Validate()
	if err == nil || !strings.Contains(err.Error(), "module is required") {
		t.Fatalf("expected module error, got %v", err)
	}
}

func TestValidateNotifyUnknownHandler(t *testing.T) {
	pb := validPlaybook()
	pb.Plays[0].Tasks[0].Notify = []string{"Does not exist"}
	err := pb.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown handler") {
		t.Fatalf("expected handler error, got %v", err)
	}
}

func TestValidateNotifyKnownHandler(t *testing.T) {
	pb := validPlaybook()
	pb.Plays[0].Tasks[0].Notify = []string{"Restart nginx"}
	if err := pb.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateDuplicateTaskIDs(t *testing.T) {
	pb := validPlaybook()
	pb.Plays[0].Tasks = append(pb.Plays[0].Tasks,
		&Task{ID: "t1", Name: "Other", Module: "ansible.builtin.debug"})
	err := pb.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate task id") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

func TestProjectValidateAggregates(t *testing.T) {
	p := &Project{
		ID:   "p1",
		Name: "demo",
		Playbooks: []*Playbook{
			validPlaybook(),
			{ID: "pb1", Name: "bad"}, // duplicate id + no plays
		},
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "duplicate playbook id") || !strings.Contains(msg, "at least one play") {
		t.Fatalf("expected aggregated problems, got %v", msg)
	}
}

func TestInventoryValidate(t *testing.T) {
	inv := &Inventory{
		ID:   "i1",
		Name: "homelab",
		Groups: []*InventoryGroup{{
			ID:   "g1",
			Name: "web",
			Hosts: []*Host{
				{ID: "h1", Name: "node01", SSHPort: 22},
				{ID: "h2", Name: "node02", SSHPort: 70000},
			},
			Children: []*InventoryGroup{
				{ID: "g2", Name: "web"}, // duplicate
			},
		}},
	}
	err := inv.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "duplicate group name") || !strings.Contains(msg, "invalid ssh port") {
		t.Fatalf("unexpected problems: %v", msg)
	}
}

func TestDeploymentPlanValidate(t *testing.T) {
	p := &DeploymentPlan{ProjectID: "p", PlaybookID: "pb", InventoryID: "i", Verbosity: 5}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "verbosity") {
		t.Fatalf("expected verbosity error, got %v", err)
	}
	p.Verbosity = 2
	if err := p.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}
