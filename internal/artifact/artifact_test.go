package artifact

import (
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/parse"
)

func demoPlaybook() *ir.Playbook {
	return &ir.Playbook{
		ID:   "pb1",
		Name: "Configure nginx",
		Plays: []*ir.Play{{
			ID: "play1", Name: "Configure nginx", Hosts: "web",
			Tasks: []*ir.Task{
				{ID: "t1", Name: "Install nginx", Module: "ansible.builtin.package",
					Args: map[string]any{"name": "nginx", "state": "present"}},
				{ID: "t2", Name: "Deploy config", Module: "ansible.builtin.template",
					Args:   map[string]any{"src": "nginx.conf.j2", "dest": "/etc/nginx/nginx.conf"},
					Notify: []string{"Restart nginx"}},
			},
			Handlers: []*ir.Task{
				{ID: "h1", Name: "Restart nginx", Module: "ansible.builtin.service",
					Args: map[string]any{"name": "nginx", "state": "restarted"}},
			},
		}},
	}
}

func TestFilenameGeneration(t *testing.T) {
	cases := map[string]string{
		"Configure nginx":            "configure-nginx.yml",
		"  Homelab / Bootstrap  ":    "homelab-bootstrap.yml",
		"Docker hosts":               "docker-hosts.yml",
		"Backup configuration (v2)":  "backup-configuration-v2.yml",
		"Disaster recovery: DR plan": "disaster-recovery-dr-plan.yml",
		"café deployment":            "café-deployment.yml",
		"配置 nginx":                   "配置-nginx.yml",
		"CON":                        "con-file.yml",
		"":                           "playbook.yml",
		"!!!":                        "playbook.yml",
	}
	for name, want := range cases {
		if got := PlaybookFilename(name, "yml"); got != want {
			t.Errorf("PlaybookFilename(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFilenameSafety(t *testing.T) {
	got := PlaybookFilename("../../etc/passwd|rm -rf *", "yml")
	for _, bad := range []string{"/", "\\", "|", "*", "..", " "} {
		if strings.Contains(got, bad) {
			t.Errorf("filename %q contains unsafe %q", got, bad)
		}
	}
	if !strings.HasSuffix(got, ".yml") {
		t.Errorf("filename lost its extension: %q", got)
	}
	// Traversal attempts cannot escape the download directory.
	if strings.HasPrefix(got, ".") {
		t.Errorf("filename starts with a dot: %q", got)
	}
}

func TestFilenameLengthBound(t *testing.T) {
	long := strings.Repeat("very-long-playbook-name ", 20)
	got := PlaybookFilename(long, "yml")
	if len(got) > 90 {
		t.Fatalf("filename too long: %d chars", len(got))
	}
}

func TestUniqueFilename(t *testing.T) {
	taken := map[string]bool{"nginx.yml": true, "nginx-2.yml": true}
	if got := UniqueFilename("nginx.yml", taken); got != "nginx-3.yml" {
		t.Fatalf("duplicate handling = %q", got)
	}
	if got := UniqueFilename("fresh.yml", taken); got != "fresh.yml" {
		t.Fatalf("unused name changed: %q", got)
	}
}

func TestExportProducesStandardAnsibleYAML(t *testing.T) {
	art, err := YAMLExporter{}.ExportPlaybook(demoPlaybook())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if art.Filename != "configure-nginx.yml" {
		t.Fatalf("filename = %q", art.Filename)
	}
	if !strings.Contains(art.ContentType, "yaml") {
		t.Fatalf("content type = %q", art.ContentType)
	}
	yaml := string(art.Data)
	for _, want := range []string{"ansible.builtin.package:", "ansible.builtin.template:", "notify:", "hosts: web"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("exported YAML missing %q:\n%s", want, yaml)
		}
	}

	// The export must be usable outside Visualible: parse it back with a
	// plain Ansible YAML parser and confirm the automation survives.
	res, err := parse.Playbook("imported", art.Data)
	if err != nil {
		t.Fatalf("exported YAML is not parseable Ansible YAML: %v", err)
	}
	if res.HasErrors() {
		t.Fatalf("exported YAML has structural errors: %+v", res.Diagnostics)
	}
	if len(res.Playbook.Plays[0].Tasks) != 2 {
		t.Fatalf("exported tasks = %d", len(res.Playbook.Plays[0].Tasks))
	}
}

// TestExportNeverContainsCredentials asserts the ownership principle: an
// export is standard Ansible content, so stored secrets — which live in
// the credential store and are referenced only by ID from inventory —
// cannot leak into it.
func TestExportNeverContainsCredentials(t *testing.T) {
	pb := demoPlaybook()
	// Even if a target machines the deployment, credentials stay in the
	// store; simulate a project whose inventory references one.
	project := &ir.Project{
		ID: "p1", Name: "Homelab",
		Playbooks: []*ir.Playbook{pb},
		Inventories: []*ir.Inventory{{
			ID: "inv1", Name: "inventory",
			Hosts: []*ir.Host{{
				ID: "h1", Name: "web01", Address: "10.0.0.11",
				SSHUser: "root", CredentialID: "cred-secret-1",
				Vars: map[string]any{"ansible_password": "should-not-be-exported"},
			}},
		}},
	}
	art, err := YAMLExporter{}.ExportPlaybook(project.Playbooks[0])
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	yaml := string(art.Data)
	for _, secret := range []string{"cred-secret-1", "should-not-be-exported", "PRIVATE KEY", "password"} {
		if strings.Contains(yaml, secret) {
			t.Errorf("credential material %q leaked into the export:\n%s", secret, yaml)
		}
	}
	// No Visualible-only metadata either: the file is plain Ansible.
	for _, marker := range []string{"visualible", "credentialId", "createdAt"} {
		if strings.Contains(strings.ToLower(yaml), strings.ToLower(marker)) {
			t.Errorf("Visualible metadata %q leaked into the export:\n%s", marker, yaml)
		}
	}
}

func TestExportRequiresPlaybook(t *testing.T) {
	if _, err := (YAMLExporter{}).ExportPlaybook(nil); err == nil {
		t.Fatal("expected an error for a missing playbook")
	}
}
