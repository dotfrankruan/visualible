package render

import (
	"strings"
	"testing"

	"github.com/visualible/visualible/internal/ir"
)

func TestRenderInventory(t *testing.T) {
	inv := &ir.Inventory{
		ID:   "inv1",
		Name: "lab",
		Hosts: []*ir.Host{
			{ID: "h0", Name: "stray"},
		},
		Groups: []*ir.InventoryGroup{
			{
				ID:   "g1",
				Name: "web",
				Vars: map[string]any{"http_port": 80},
				Hosts: []*ir.Host{
					{ID: "h1", Name: "node01", Address: "192.168.1.10", SSHUser: "debian", SSHPort: 22,
						Vars: map[string]any{"rack": "a"}},
					{ID: "h2", Name: "node02", Address: "192.168.1.11"},
				},
				Children: []*ir.InventoryGroup{
					{ID: "g2", Name: "edge", Hosts: []*ir.Host{{ID: "h3", Name: "edge01"}}},
				},
			},
		},
	}
	out, err := Inventory(inv)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	y := string(out)
	for _, want := range []string{
		"all:",
		"children:",
		"web:",
		"node01:",
		"ansible_host: 192.168.1.10",
		"ansible_user: debian",
		"ansible_port: 22",
		"rack: a",
		"http_port: 80",
		"edge:",
		"edge01:",
		"stray:",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("inventory output missing %q\n%s", want, y)
		}
	}
	// No credential material may leak into inventory.
	if strings.Contains(y, "assword") || strings.Contains(y, "private_key") {
		t.Errorf("inventory must not contain credentials\n%s", y)
	}
}

func TestRenderInventoryRejectsInvalid(t *testing.T) {
	inv := &ir.Inventory{ID: "x", Name: "", Groups: nil}
	if _, err := Inventory(inv); err == nil {
		t.Fatal("expected validation error")
	}
}
