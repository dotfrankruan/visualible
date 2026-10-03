//go:build integration

// Integration tests against the real, locally installed Ansible. They are
// excluded from the normal test suite (no network/Ansible requirement):
//
//	go test -tags integration ./internal/ansible/ -run Integration -v
//
// Set VISUALIBLE_IT_PREFIX to limit the module sweep (default
// "ansible.builtin.") and VISUALIBLE_IT_MAX to cap its size (0 = no cap).
package ansible

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func integrationDiscovery(t *testing.T) *Discovery {
	t.Helper()
	inst, err := Detect(context.Background())
	if err != nil {
		t.Skipf("ansible not available: %v", err)
	}
	return NewDiscoveryWith(inst, NewDocClient(inst), nil)
}

func TestIntegrationModuleList(t *testing.T) {
	d := integrationDiscovery(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	raw, err := d.doc.ListJSON(ctx)
	if err != nil {
		t.Fatalf("ansible-doc -l -j: %v", err)
	}
	mods, err := NormalizeModuleList(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(mods) < 100 {
		t.Fatalf("suspiciously few modules: %d", len(mods))
	}
	for _, m := range mods {
		if m.FQCN == "" || m.Name == "" {
			t.Fatalf("incomplete summary: %+v", m)
		}
		if parts := strings.Split(m.FQCN, "."); len(parts) == 3 && m.Collection == "" {
			t.Fatalf("collection not derived: %+v", m)
		}
	}
	t.Logf("discovered %d modules", len(mods))
}

// TestIntegrationNormalizeAll sweeps real ansible-doc output for modules
// through NormalizeModuleDoc and fails on any normalization error. This is
// the guard against schema drift between Visualible and ansible-core.
func TestIntegrationNormalizeAll(t *testing.T) {
	d := integrationDiscovery(t)
	ctx := context.Background()

	raw, err := d.doc.ListJSON(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	mods, err := NormalizeModuleList(raw)
	if err != nil {
		t.Fatalf("normalize list: %v", err)
	}

	prefix := os.Getenv("VISUALIBLE_IT_PREFIX")
	if prefix == "" {
		prefix = "ansible.builtin."
	}
	max, _ := strconv.Atoi(os.Getenv("VISUALIBLE_IT_MAX"))

	var targets []string
	for _, m := range mods {
		if prefix == "*" || strings.HasPrefix(m.FQCN, prefix) {
			targets = append(targets, m.FQCN)
		}
	}
	if max > 0 && len(targets) > max {
		targets = targets[:max]
	}
	t.Logf("sweeping %d modules (prefix %q, max %d)", len(targets), prefix, max)

	failures := 0
	for _, fqcn := range targets {
		raw, err := d.doc.DocJSON(ctx, fqcn)
		if err != nil {
			// Some plugins fail to load docs (broken deps etc.); that is
			// an Ansible-side condition, not a normalization bug.
			t.Logf("WARN doc fetch failed %s: %v", fqcn, err)
			continue
		}
		schema, err := NormalizeModuleDoc(raw)
		if err != nil {
			t.Errorf("normalize %s: %v", fqcn, err)
			failures++
			continue
		}
		if schema.FQCN == "" || schema.Name == "" {
			t.Errorf("normalize %s: incomplete schema", fqcn)
			failures++
		}
	}
	if failures > 0 {
		t.Fatalf("%d/%d modules failed normalization", failures, len(targets))
	}
}
