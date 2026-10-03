package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dotfrankruan/visualible/internal/ir"
)

func multiPlaybookProject() *ir.Project {
	return &ir.Project{
		ID:   "p1",
		Name: "Homelab",
		Playbooks: []*ir.Playbook{
			{
				ID: "pb1", Name: "Bootstrap Debian",
				Plays: []*ir.Play{{ID: "pl1", Name: "bootstrap", Hosts: "all",
					Tasks: []*ir.Task{{ID: "t1", Name: "apt", Module: "ansible.builtin.package",
						Args: map[string]any{"name": "curl", "state": "present"}}}}},
			},
			{
				ID: "pb2", Name: "Configure nginx",
				Plays: []*ir.Play{{ID: "pl2", Name: "nginx", Hosts: "web",
					Tasks: []*ir.Task{{ID: "t2", Name: "nginx", Module: "ansible.builtin.package",
						Args: map[string]any{"name": "nginx", "state": "present"}}},
					Handlers: []*ir.Task{{ID: "h1", Name: "restart nginx", Module: "ansible.builtin.service",
						Args: map[string]any{"name": "nginx", "state": "restarted"}}}}},
			},
		},
		Inventories: []*ir.Inventory{{
			ID: "inv1", Name: "lab",
			Hosts: []*ir.Host{{ID: "h1", Name: "web01", CredentialID: "cred-1"}},
		}},
	}
}

func TestMultiplePlaybooksRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	project := multiPlaybookProject()
	if err := s.SaveProject(ctx, project); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Playbooks) != 2 {
		t.Fatalf("playbooks = %d", len(got.Playbooks))
	}
	if got.Playbooks[0].Name != "Bootstrap Debian" || got.Playbooks[1].Name != "Configure nginx" {
		t.Fatalf("names wrong: %s / %s", got.Playbooks[0].Name, got.Playbooks[1].Name)
	}
	// Each document keeps its own IR, and shared project state (targets and
	// credential references) is untouched.
	if got.Playbooks[1].Plays[0].Hosts != "web" {
		t.Fatalf("playbook IR lost: %+v", got.Playbooks[1].Plays[0])
	}
	if got.Inventories[0].Hosts[0].CredentialID != "cred-1" {
		t.Fatalf("credential reference lost: %+v", got.Inventories[0].Hosts[0])
	}
}

func TestLegacyProjectMigrationOnLoad(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// A project persisted before playbooks carried their own metadata: no
	// description/timestamps on the playbook, and no inventories array.
	legacy := map[string]any{
		"id": "old1", "name": "Legacy project",
		"createdAt": "2024-01-02T03:04:05Z", "updatedAt": "2024-03-04T05:06:07Z",
		"playbooks": []any{map[string]any{
			"id": "oldpb", "name": "Legacy playbook",
			"plays": []any{map[string]any{
				"id": "lp", "name": "legacy", "hosts": "all",
				"tasks": []any{map[string]any{
					"id": "lt", "name": "old task", "module": "ansible.builtin.debug"}},
			}},
		}},
	}
	raw, _ := json.Marshal(legacy)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"old1", "Legacy project", string(raw),
		"2024-01-02T03:04:05Z", "2024-03-04T05:06:07Z"); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	got, err := s.GetProject(ctx, "old1")
	if err != nil {
		t.Fatalf("legacy project must still open: %v", err)
	}
	if len(got.Playbooks) != 1 || got.Playbooks[0].Name != "Legacy playbook" {
		t.Fatalf("legacy playbook lost: %+v", got.Playbooks)
	}
	// Timestamps are derived from the project, so the Library can sort it.
	if got.Playbooks[0].CreatedAt.IsZero() || got.Playbooks[0].UpdatedAt.IsZero() {
		t.Fatalf("legacy playbook gained no timestamps: %+v", got.Playbooks[0])
	}
	if got.Playbooks[0].UpdatedAt.Year() != 2024 {
		t.Fatalf("derived timestamp should come from the project: %v", got.Playbooks[0].UpdatedAt)
	}
	// Inventories default to an empty list rather than nil.
	if got.Inventories == nil {
		t.Fatal("inventories should be normalised to an empty list")
	}
	// The IR itself is preserved exactly.
	if got.Playbooks[0].Plays[0].Tasks[0].Module != "ansible.builtin.debug" {
		t.Fatalf("legacy IR lost: %+v", got.Playbooks[0].Plays[0].Tasks[0])
	}
}

func TestPerPlaybookModificationTimes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	project := multiPlaybookProject()
	if err := s.SaveProject(ctx, project); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	first := map[string]time.Time{}
	for _, pb := range got.Playbooks {
		first[pb.ID] = pb.UpdatedAt
	}
	time.Sleep(5 * time.Millisecond)

	// Change only the second playbook.
	got.Playbooks[1].Plays[0].Tasks[0].Args["state"] = "latest"
	if err := s.SaveProject(ctx, got); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	after, err := s.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	byID := map[string]*ir.Playbook{}
	for _, pb := range after.Playbooks {
		byID[pb.ID] = pb
	}
	if !byID["pb2"].UpdatedAt.After(first["pb2"]) {
		t.Errorf("edited playbook should be stamped newer: %v -> %v", first["pb2"], byID["pb2"].UpdatedAt)
	}
	if !byID["pb1"].UpdatedAt.Equal(first["pb1"]) {
		t.Errorf("untouched playbook must keep its timestamp: %v -> %v", first["pb1"], byID["pb1"].UpdatedAt)
	}
}

func TestProjectMetaPlaybookSummaries(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveProject(ctx, multiPlaybookProject()); err != nil {
		t.Fatalf("save: %v", err)
	}
	list, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("projects = %d", len(list))
	}
	summaries := list[0].Playbooks
	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v", summaries)
	}
	if summaries[0].Steps != 1 || summaries[1].Steps != 2 {
		t.Fatalf("step counts wrong: %+v", summaries)
	}
	for _, sum := range summaries {
		if sum.UpdatedAt.IsZero() {
			t.Errorf("summary without a modified time: %+v", sum)
		}
	}
}

func TestPlaybookStepCount(t *testing.T) {
	pb := multiPlaybookProject().Playbooks[1]
	if got := pb.StepCount(); got != 2 {
		t.Fatalf("StepCount() = %d, want 2 (task + handler)", got)
	}
	if got := (&ir.Playbook{}).StepCount(); got != 0 {
		t.Fatalf("empty StepCount() = %d", got)
	}
}
