package ansible

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestNormalizeModuleList(t *testing.T) {
	list, err := NormalizeModuleList(readFixture(t, "doc-list.json"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("expected 5 modules, got %d", len(list))
	}
	// Sorted by FQCN.
	if list[0].FQCN != "ansible.builtin.apt" {
		t.Fatalf("expected sorted output, first = %q", list[0].FQCN)
	}
	var docker *ModuleSummary
	for i := range list {
		if list[i].FQCN == "community.docker.docker_container" {
			docker = &list[i]
		}
	}
	if docker == nil {
		t.Fatal("docker_container missing")
	}
	if docker.Collection != "community.docker" {
		t.Fatalf("collection = %q", docker.Collection)
	}
	if docker.Name != "docker_container" {
		t.Fatalf("name = %q", docker.Name)
	}
	if docker.ShortDescription == "" {
		t.Fatal("short description missing")
	}
}

func TestNormalizeModuleDoc(t *testing.T) {
	schema, err := NormalizeModuleDoc(readFixture(t, "doc-apt.json"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if schema.FQCN != "ansible.builtin.apt" || schema.Name != "apt" {
		t.Fatalf("identity wrong: %q %q", schema.FQCN, schema.Name)
	}
	if schema.Collection != "ansible.builtin" {
		t.Fatalf("collection = %q", schema.Collection)
	}
	if len(schema.Description) != 2 {
		t.Fatalf("description lines = %d", len(schema.Description))
	}
	if len(schema.Options) != 9 {
		t.Fatalf("options = %d, want 9", len(schema.Options))
	}

	state := schema.Options["state"]
	if state == nil {
		t.Fatal("state option missing")
	}
	if state.Type != "str" {
		t.Fatalf("state type = %q", state.Type)
	}
	if state.Default != "present" {
		t.Fatalf("state default = %v", state.Default)
	}
	if len(state.Choices) != 5 || state.Choices[0] != "absent" {
		t.Fatalf("state choices = %v", state.Choices)
	}

	name := schema.Options["name"]
	if name == nil || name.Elements != "str" || name.Type != "list" {
		t.Fatalf("name option wrong: %+v", name)
	}
	if len(name.Aliases) != 2 || name.Aliases[0] != "pkg" {
		t.Fatalf("name aliases = %v", name.Aliases)
	}

	allow := schema.Options["allow_downgrade"]
	if allow.Default != false || allow.VersionAdded != "2.12" {
		t.Fatalf("allow_downgrade wrong: %+v", allow)
	}

	if len(schema.SeeAlso) != 2 || schema.SeeAlso[0] != "ansible.builtin.apt_repository" {
		t.Fatalf("seealso = %v", schema.SeeAlso)
	}
}

func TestNormalizeModuleDocNestedSuboptions(t *testing.T) {
	schema, err := NormalizeModuleDoc(readFixture(t, "doc-docker-container.json"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	networks := schema.Options["networks"]
	if networks == nil {
		t.Fatal("networks option missing")
	}
	if networks.Type != "list" || networks.Elements != "dict" {
		t.Fatalf("networks type wrong: %q/%q", networks.Type, networks.Elements)
	}
	if len(networks.Suboptions) != 3 {
		t.Fatalf("networks suboptions = %d", len(networks.Suboptions))
	}
	nn := networks.Suboptions["name"]
	if nn == nil || !nn.Required || nn.Type != "str" {
		t.Fatalf("networks.name wrong: %+v", nn)
	}

	// "options" as an alternate nesting key must also be recognized.
	logcfg := schema.Options["log_config"]
	if logcfg == nil || len(logcfg.Suboptions) != 1 || logcfg.Suboptions["driver"] == nil {
		t.Fatalf("log_config suboptions not recognized: %+v", logcfg)
	}

	// Description given as a bare string must normalize to a list.
	if len(networks.Description) != 1 {
		t.Fatalf("string description not normalized: %v", networks.Description)
	}
}

func TestValidateFQCN(t *testing.T) {
	valid := []string{"ansible.builtin.apt", "community.docker.docker_container", "kubernetes.core.k8s"}
	for _, v := range valid {
		if err := ValidateFQCN(v); err != nil {
			t.Errorf("%q should be valid: %v", v, err)
		}
	}
	invalid := []string{"apt", "--json", "-l", "Ansible.Builtin.Apt", "a.b", "a.b.c.d", "a b.c.d", ""}
	for _, v := range invalid {
		if err := ValidateFQCN(v); err == nil {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"ansible [core 2.16.3]\n  config file = /etc/ansible.cfg": "2.16.3",
		"ansible [core 2.15.12]":                                   "2.15.12",
		"garbage":                                                  "",
	}
	for in, want := range cases {
		if got := parseVersion(in); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectWithMissing(t *testing.T) {
	_, err := DetectWith(context.Background(),
		func(file string) (string, error) { return "", errors.New("not found") },
		nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDetectWithSuccess(t *testing.T) {
	inst, err := DetectWith(context.Background(),
		func(file string) (string, error) { return "/usr/local/bin/" + file, nil },
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("ansible [core 2.16.3]\n  config file = None\n"), nil
		})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if inst.Version != "2.16.3" {
		t.Fatalf("version = %q", inst.Version)
	}
	if inst.AnsibleDocPath != "/usr/local/bin/ansible-doc" {
		t.Fatalf("doc path = %q", inst.AnsibleDocPath)
	}
}

func TestDocClientUsesStructuredArgs(t *testing.T) {
	var gotName string
	var gotArgs []string
	c := NewDocClientWith("/fake/ansible-doc",
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			gotName = name
			gotArgs = args
			return []byte("{}"), nil
		})
	if _, err := c.DocJSON(context.Background(), "ansible.builtin.apt"); err != nil {
		t.Fatalf("doc: %v", err)
	}
	if gotName != "/fake/ansible-doc" {
		t.Fatalf("name = %q", gotName)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "-j" || gotArgs[1] != "ansible.builtin.apt" {
		t.Fatalf("args = %v", gotArgs)
	}
	// FQCN injection attempts must be refused before exec.
	if _, err := c.DocJSON(context.Background(), "--tactic"); err == nil {
		t.Fatal("expected validation error for flag-like module name")
	}
}

func TestDiscoveryCaching(t *testing.T) {
	cache, err := OpenCache(t.TempDir())
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	calls := 0
	doc := NewDocClientWith("/fake/ansible-doc",
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			calls++
			if args[0] == "-l" {
				return readFixture(t, "doc-list.json"), nil
			}
			return readFixture(t, "doc-apt.json"), nil
		})
	d := NewDiscoveryWith(&Installation{Path: "/fake"}, doc, cache)

	list, err := d.Modules(context.Background(), false)
	if err != nil || len(list) != 5 {
		t.Fatalf("modules: %v (%d)", err, len(list))
	}
	if calls != 1 {
		t.Fatalf("expected 1 exec, got %d", calls)
	}
	// Second call must hit the cache.
	if _, err := d.Modules(context.Background(), false); err != nil {
		t.Fatalf("modules cached: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected cache hit, execs = %d", calls)
	}

	s, err := d.Module(context.Background(), "ansible.builtin.apt", false)
	if err != nil {
		t.Fatalf("module: %v", err)
	}
	if len(s.Options) != 9 {
		t.Fatalf("options = %d", len(s.Options))
	}
	if calls != 2 {
		t.Fatalf("expected 2 execs, got %d", calls)
	}
	if _, err := d.Module(context.Background(), "ansible.builtin.apt", false); err != nil {
		t.Fatalf("module cached: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected schema cache hit, execs = %d", calls)
	}

	// Cache must survive a reload.
	cache2, err := OpenCache(cache.dir)
	if err != nil {
		t.Fatalf("reopen cache: %v", err)
	}
	if _, ok := cache2.Schema("ansible.builtin.apt"); !ok {
		t.Fatal("schema not persisted")
	}
	if l, ok := cache2.List(); !ok || len(l) != 5 {
		t.Fatal("list not persisted")
	}
}

func TestDiscoveryWithoutAnsible(t *testing.T) {
	cache, err := OpenCache(t.TempDir())
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	d := NewDiscoveryWith(nil, nil, cache)
	if d.Status().Available {
		t.Fatal("expected unavailable status")
	}
	if _, err := d.Modules(context.Background(), false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
