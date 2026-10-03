package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dotfrankruan/visualible/internal/ir"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func demoProject() *ir.Project {
	return &ir.Project{
		ID:   "p1",
		Name: "homelab",
		Playbooks: []*ir.Playbook{{
			ID:   "pb1",
			Name: "webserver",
			Plays: []*ir.Play{{
				ID:    "play1",
				Name:  "Configure webserver",
				Hosts: "web",
				Tasks: []*ir.Task{
					{ID: "t1", Name: "Install nginx", Module: "ansible.builtin.apt",
						Args: map[string]any{"name": "nginx", "state": "present"}},
				},
			}},
		}},
		Inventories: []*ir.Inventory{{
			ID:   "inv1",
			Name: "lab",
			Groups: []*ir.InventoryGroup{{
				ID:    "g1",
				Name:  "web",
				Hosts: []*ir.Host{{ID: "h1", Name: "node01", SSHPort: 22}},
			}},
		}},
	}
}

func TestSaveAndGetRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	p := demoProject()
	if err := s.SaveProject(ctx, p); err != nil {
		t.Fatalf("save: %v", err)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatal("timestamps not set")
	}

	got, err := s.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "homelab" || len(got.Playbooks) != 1 || len(got.Inventories) != 1 {
		t.Fatalf("unexpected project: %+v", got)
	}
	task := got.Playbooks[0].Plays[0].Tasks[0]
	if task.Module != "ansible.builtin.apt" || task.Args["state"] != "present" {
		t.Fatalf("task corrupted: %+v", task)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	s := openTest(t)
	p := demoProject()
	p.Playbooks[0].Plays[0].Hosts = ""
	if err := s.SaveProject(context.Background(), p); err == nil {
		t.Fatal("expected validation refusal")
	}
}

func TestUpdatePreservesCreatedAt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := demoProject()
	if err := s.SaveProject(ctx, p); err != nil {
		t.Fatalf("save: %v", err)
	}
	created := p.CreatedAt
	time.Sleep(2 * time.Millisecond)
	p.Name = "renamed"
	if err := s.SaveProject(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !p.CreatedAt.Equal(created) {
		t.Fatalf("createdAt changed: %v vs %v", p.CreatedAt, created)
	}
	got, err := s.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "renamed" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestListProjects(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		p := demoProject()
		p.ID = id
		p.Name = "proj-" + id
		if err := s.SaveProject(ctx, p); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	list, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d", len(list))
	}
}

func TestDeleteProject(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveProject(ctx, demoProject()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.DeleteProject(ctx, "p1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetProject(ctx, "p1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := s.DeleteProject(ctx, "p1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}
