package render

import (
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
)

func demoPlaybook() *ir.Playbook {
	become := true
	retries := 3
	delay := 5
	return &ir.Playbook{
		ID:   "pb1",
		Name: "webserver",
		Plays: []*ir.Play{{
			ID:     "play1",
			Name:   "Configure webserver",
			Hosts:  "web",
			Become: true,
			Tags:   []string{"web"},
			Tasks: []*ir.Task{
				{
					ID:     "t1",
					Name:   "Install nginx",
					Module: "ansible.builtin.apt",
					Args:   map[string]any{"name": "nginx", "state": "present"},
				},
				{
					ID:       "t2",
					Name:     "Deploy configuration",
					Module:   "ansible.builtin.template",
					Args:     map[string]any{"src": "nginx.conf.j2", "dest": "/etc/nginx/nginx.conf"},
					Notify:   []string{"Restart nginx"},
					Become:   &become,
					Register: "nginx_conf",
					When:     "nginx_enabled | bool",
					Tags:     []string{"config"},
				},
				{
					ID:      "t3",
					Name:    "Flaky operation",
					Module:  "ansible.builtin.uri",
					Args:    map[string]any{"url": "http://localhost/health"},
					Until:   "result.status == 200",
					Retries: &retries,
					Delay:   &delay,
				},
			},
			Handlers: []*ir.Task{
				{
					ID:     "h1",
					Name:   "Restart nginx",
					Module: "ansible.builtin.systemd_service",
					Args:   map[string]any{"name": "nginx", "state": "restarted"},
				},
			},
		}},
	}
}

func renderString(t *testing.T, pb *ir.Playbook) string {
	t.Helper()
	out, err := Playbook(pb)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(out)
}

func TestRenderDemoPlaybook(t *testing.T) {
	got := renderString(t, demoPlaybook())

	checks := []string{
		"- name: Configure webserver",
		"hosts: web",
		"become: true",
		"- web",
		"- name: Install nginx",
		"ansible.builtin.apt:",
		"state: present",
		"- name: Deploy configuration",
		"ansible.builtin.template:",
		"src: nginx.conf.j2",
		"dest: /etc/nginx/nginx.conf",
		"register: nginx_conf",
		"when: nginx_enabled | bool",
		"- Restart nginx",
		"handlers:",
		"ansible.builtin.systemd_service:",
		"state: restarted",
		"until: result.status == 200",
		"retries: 3",
		"delay: 5",
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("output missing %q\n---\n%s", c, got)
		}
	}
}

func TestRenderKeyOrder(t *testing.T) {
	got := renderString(t, demoPlaybook())
	// name precedes module, module precedes when, when precedes notify.
	idx := func(s string) int { return strings.Index(got, s) }
	nameI := idx("- name: Deploy configuration")
	modI := idx("ansible.builtin.template:")
	whenI := idx("when: nginx_enabled | bool")
	notifyI := idx("notify:")
	if !(nameI >= 0 && nameI < modI && modI < whenI && whenI < notifyI) {
		t.Errorf("unexpected key order (%d %d %d %d)\n%s", nameI, modI, whenI, notifyI, got)
	}
	// Play-level order: name, hosts, become before tasks.
	playName := idx("- name: Configure webserver")
	hosts := idx("hosts: web")
	tasks := idx("tasks:")
	if !(playName >= 0 && playName < hosts && hosts < tasks) {
		t.Errorf("unexpected play key order\n%s", got)
	}
}

func TestRenderRejectsInvalid(t *testing.T) {
	pb := demoPlaybook()
	pb.Plays[0].Hosts = ""
	if _, err := Playbook(pb); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRenderRejectsUnsupportedBlock(t *testing.T) {
	pb := demoPlaybook()
	pb.Plays[0].Tasks[0].Block = []*ir.Task{{ID: "b1", Name: "inner", Module: "ansible.builtin.debug"}}
	_, err := Playbook(pb)
	if err == nil {
		t.Fatal("expected unsupported error")
	}
	ue, ok := err.(*UnsupportedError)
	if !ok {
		t.Fatalf("expected UnsupportedError, got %T", err)
	}
	if !strings.Contains(ue.Error(), "block/rescue/always") {
		t.Fatalf("unexpected message: %v", ue)
	}
}

func TestRenderEmptyArgs(t *testing.T) {
	pb := demoPlaybook()
	pb.Plays[0].Tasks[0].Args = nil
	got := renderString(t, pb)
	if !strings.Contains(got, "ansible.builtin.apt: {}") {
		t.Errorf("expected empty args rendered as {}\n%s", got)
	}
}
