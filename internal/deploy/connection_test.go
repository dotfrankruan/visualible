package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/ir"
)

func TestConnectionTestSuccess(t *testing.T) {
	runner := &fakeRunner{
		// Real ansible -m ping output shape.
		stdout: `web01 | SUCCESS => {"ansible_facts": {"discovered_interpreter_python": "/usr/bin/python3"}, "changed": false, "ping": "pong"}`,
	}
	b := testBackend(runner)
	host := &ir.Host{ID: "h1", Name: "web01", Address: "10.0.0.11", SSHUser: "root", SSHPort: 22}
	res := b.TestConnection(context.Background(), host, credResolver())
	if !res.OK {
		t.Fatalf("expected success, got %+v", res)
	}
	if res.Message != "Connection works." {
		t.Fatalf("message = %q", res.Message)
	}
	if res.Host != "web01" || res.Address != "10.0.0.11" {
		t.Fatalf("identity wrong: %+v", res)
	}
	// The ping must run in an isolated workspace with a one-host inventory.
	if len(runner.runs) != 1 {
		t.Fatalf("runs = %d", len(runner.runs))
	}
	args := strings.Join(runner.runs[0].Args, " ")
	if !strings.Contains(args, "-m ping") {
		t.Fatalf("args = %v", runner.runs[0].Args)
	}
}

func TestConnectionTestFailures(t *testing.T) {
	cases := map[string]string{
		`web01 | UNREACHABLE! => {"msg": "Failed to connect to the host via ssh: Permission denied (publickey)."}`: "SSH login was refused",
		`web01 | UNREACHABLE! => {"msg": "Host key verification failed."}`:                                         "host key is not trusted",
		`web01 | UNREACHABLE! => {"msg": "ssh: connect to host 10.0.0.11 port 22: Connection refused"}`:            "refused the SSH connection",
		`web01 | UNREACHABLE! => {"msg": "ssh: connect to host 10.0.0.11 port 22: No route to host"}`:              "not reachable on the network",
		`web01 | UNREACHABLE! => {"msg": "ssh: Could not resolve hostname web01: Name or service not known"}`:      "could not be resolved",
		`web01 | UNREACHABLE! => {"msg": "Connection timed out during banner exchange"}`:                           "timed out",
	}
	for out, want := range cases {
		runner := &fakeRunner{exitCode: 4, stdout: out}
		b := testBackend(runner)
		res := b.TestConnection(context.Background(), &ir.Host{Name: "web01", Address: "10.0.0.11"}, credResolver())
		if res.OK {
			t.Errorf("expected failure for %q", out)
			continue
		}
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not explain %q", res.Message, want)
		}
		if !strings.Contains(res.Details, "UNREACHABLE") {
			t.Errorf("raw details not preserved for %q: %q", out, res.Details)
		}
	}
}

func TestConnectionTestWithoutCredentialResolution(t *testing.T) {
	b := testBackend(&fakeRunner{})
	host := &ir.Host{Name: "node01", CredentialID: "missing"}
	res := b.TestConnection(context.Background(), host, credResolver())
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.Details == "" {
		t.Fatal("expected raw details")
	}
}

func TestConnectionTestNoAnsible(t *testing.T) {
	b := NewAnsibleBackendWith(nil, &fakeRunner{})
	res := b.TestConnection(context.Background(), &ir.Host{Name: "web01"}, credResolver())
	if res.OK || !strings.Contains(res.Message, "not available") {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestPreflightAcrossInventory(t *testing.T) {
	runner := &fakeRunner{stdout: `web01 | SUCCESS => {"ping": "pong"}`}
	b := testBackend(runner)
	inv := &ir.Inventory{
		ID: "inv", Name: "lab",
		Hosts: []*ir.Host{{ID: "h0", Name: "stray"}},
		Groups: []*ir.InventoryGroup{{
			ID: "g1", Name: "web",
			Hosts: []*ir.Host{
				{ID: "h1", Name: "web01", Address: "10.0.0.11", SSHPort: 22},
				{ID: "h2", Name: "web02", Address: "10.0.0.12"},
			},
			Children: []*ir.InventoryGroup{
				{ID: "g2", Name: "deep", Hosts: []*ir.Host{{ID: "h3", Name: "web03"}}},
			},
		}},
	}
	res := b.TestConnections(context.Background(), inv, credResolver())
	if res.Total != 4 || len(res.Hosts) != 4 {
		t.Fatalf("flattening wrong: %+v", res)
	}
	if res.Reachable != 4 {
		t.Fatalf("reachable = %d", res.Reachable)
	}
	// Address includes the port when configured.
	for _, h := range res.Hosts {
		if h.Name == "web01" && h.Address != "10.0.0.11:22" {
			t.Fatalf("address = %q", h.Address)
		}
	}
}

func TestPreflightEmptyInventory(t *testing.T) {
	b := testBackend(&fakeRunner{})
	res := b.TestConnections(context.Background(), &ir.Inventory{ID: "i", Name: "n"}, credResolver())
	if res.Total != 0 || res.Reachable != 0 {
		t.Fatalf("unexpected: %+v", res)
	}
}
