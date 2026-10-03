package parse

import (
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/render"
)

const demoYAML = `
- name: Configure webserver
  hosts: web
  become: true
  tags: [web]
  vars:
    http_port: 80
  tasks:
    - name: Install nginx
      ansible.builtin.apt:
        name: nginx
        state: present
    - name: Deploy configuration
      ansible.builtin.template:
        src: nginx.conf.j2
        dest: /etc/nginx/nginx.conf
      notify: Restart nginx
      when: nginx_enabled | bool
      register: nginx_conf
      retries: 3
      delay: 5
      until: nginx_conf is succeeded
  handlers:
    - name: Restart nginx
      ansible.builtin.systemd_service:
        name: nginx
        state: restarted
`

func TestParseDemoPlaybook(t *testing.T) {
	res, err := Playbook("demo", []byte(demoYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.HasErrors() {
		t.Fatalf("unexpected errors: %+v", res.Diagnostics)
	}
	pb := res.Playbook
	if len(pb.Plays) != 1 {
		t.Fatalf("plays = %d", len(pb.Plays))
	}
	play := pb.Plays[0]
	if play.Name != "Configure webserver" || play.Hosts != "web" || !play.Become {
		t.Fatalf("play wrong: %+v", play)
	}
	if play.Vars["http_port"] != 80 {
		t.Fatalf("vars wrong: %v", play.Vars)
	}
	if len(play.Tags) != 1 || play.Tags[0] != "web" {
		t.Fatalf("tags wrong: %v", play.Tags)
	}
	if len(play.Tasks) != 2 || len(play.Handlers) != 1 {
		t.Fatalf("tasks/handlers wrong: %d/%d", len(play.Tasks), len(play.Handlers))
	}

	t1 := play.Tasks[0]
	if t1.Module != "ansible.builtin.apt" || t1.Args["state"] != "present" {
		t.Fatalf("task1 wrong: %+v", t1)
	}
	if t1.ID == "" {
		t.Fatal("task id not generated")
	}

	t2 := play.Tasks[1]
	if t2.When != "nginx_enabled | bool" || t2.Register != "nginx_conf" {
		t.Fatalf("task2 wrong: %+v", t2)
	}
	if len(t2.Notify) != 1 || t2.Notify[0] != "Restart nginx" {
		t.Fatalf("notify wrong: %v", t2.Notify)
	}
	if t2.Retries == nil || *t2.Retries != 3 || t2.Delay == nil || *t2.Delay != 5 {
		t.Fatalf("retries/delay wrong: %+v", t2)
	}

	// Must validate against handler notify references.
	if err := pb.Validate(); err != nil {
		t.Fatalf("parsed playbook invalid: %v", err)
	}
}

func TestRoundTripPreservesSemantics(t *testing.T) {
	res, err := Playbook("demo", []byte(demoYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := render.Playbook(res.Playbook)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	y := string(out)
	for _, want := range []string{
		"- name: Configure webserver",
		"hosts: web",
		"become: true",
		"http_port: 80",
		"ansible.builtin.apt:",
		"state: present",
		"ansible.builtin.template:",
		"notify:",
		"- Restart nginx",
		"retries: 3",
		"ansible.builtin.systemd_service:",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("round-trip output missing %q\n%s", want, y)
		}
	}

	// Re-parse the rendered output: semantic stability.
	res2, err := Playbook("demo", out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if res2.HasErrors() {
		t.Fatalf("re-parse errors: %+v", res2.Diagnostics)
	}
	if len(res2.Playbook.Plays[0].Tasks) != 2 {
		t.Fatalf("re-parse tasks wrong")
	}
}

func TestParseMultipleModuleKeysDiagnostic(t *testing.T) {
	yaml := `
- name: x
  hosts: all
  tasks:
    - name: ambiguous
      ansible.builtin.debug:
        msg: hi
      ansible.builtin.assert:
        that: true
`
	res, err := Playbook("t", []byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !res.HasErrors() {
		t.Fatal("expected error diagnostic for ambiguous module keys")
	}
	found := false
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, "multiple candidate module keys") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics: %+v", res.Diagnostics)
	}
	// Data must be preserved, not dropped.
	task := res.Playbook.Plays[0].Tasks[0]
	if task.Extras["ansible.builtin.debug"] == nil || task.Extras["ansible.builtin.assert"] == nil {
		t.Fatalf("extras lost: %+v", task.Extras)
	}
}

func TestParseUnknownKeysPreserved(t *testing.T) {
	yaml := `
- name: x
  hosts: all
  tasks:
    - name: with custom key
      ansible.builtin.command:
        cmd: /bin/true
      some_custom_key: custom_value
`
	res, err := Playbook("t", []byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// One extra key + one module key: ambiguous, preserved via extras.
	task := res.Playbook.Plays[0].Tasks[0]
	if task.Extras["some_custom_key"] != "custom_value" {
		t.Fatalf("custom key lost: %+v", task)
	}
}

func TestParseLegacyActionWarns(t *testing.T) {
	yaml := `
- name: x
  hosts: all
  tasks:
    - name: legacy
      action: ping
`
	res, err := Playbook("t", []byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	hasLegacyWarning := false
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, "legacy") {
			hasLegacyWarning = true
		}
	}
	if !hasLegacyWarning {
		t.Fatalf("expected legacy warning: %+v", res.Diagnostics)
	}
}

func TestParseBlockPreservedWithWarning(t *testing.T) {
	yaml := `
- name: x
  hosts: all
  tasks:
    - name: guarded
      block:
        - name: inner
          ansible.builtin.debug:
            msg: hello
`
	res, err := Playbook("t", []byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	task := res.Playbook.Plays[0].Tasks[0]
	if len(task.Block) != 1 || task.Block[0].Module != "ansible.builtin.debug" {
		t.Fatalf("block not parsed: %+v", task)
	}
	warned := false
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, "block/rescue/always") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected block warning: %+v", res.Diagnostics)
	}
}

func TestParseRejectsNonList(t *testing.T) {
	if _, err := Playbook("t", []byte("name: not a playbook\n")); err == nil {
		t.Fatal("expected error for non-list document")
	}
	if _, err := Playbook("t", []byte("")); err == nil {
		t.Fatal("expected error for empty document")
	}
	if _, err := Playbook("t", []byte("- - - [")); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseRolesAndIncludeTasks(t *testing.T) {
	yaml := `
- name: x
  hosts: all
  roles:
    - common
    - role: nginx
      vars:
        port: 8080
  tasks:
    - name: include more
      include_tasks: more.yml
`
	res, err := Playbook("t", []byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	play := res.Playbook.Plays[0]
	if len(play.Roles) != 2 || play.Roles[0].Role != "common" {
		t.Fatalf("roles wrong: %+v", play.Roles)
	}
	if play.Roles[1].Role != "nginx" || play.Roles[1].Vars["port"] != 8080 {
		t.Fatalf("role vars wrong: %+v", play.Roles[1])
	}
	if play.Tasks[0].IncludeTasks != "more.yml" {
		t.Fatalf("include_tasks wrong: %+v", play.Tasks[0])
	}
	if err := res.Playbook.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
