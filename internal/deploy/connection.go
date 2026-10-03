package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/render"
)

// ConnectionResult reports whether Visualible can reach one machine.
// Details carries raw Ansible output for the "Show details" affordance;
// Message is user-facing language.
type ConnectionResult struct {
	Host       string `json:"host"`
	Address    string `json:"address,omitempty"`
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	Details    string `json:"details,omitempty"`
	DurationMS int64  `json:"durationMs"`
}

// ConnectionTester is implemented by backends that can verify
// connectivity to a single machine without deploying anything. Backends
// without inbound SSH (or agent-based backends) may omit it.
type ConnectionTester interface {
	TestConnection(ctx context.Context, host *ir.Host, secrets SecretResolver) ConnectionResult
	TestConnections(ctx context.Context, inventory *ir.Inventory, secrets SecretResolver) PreflightResult
}

const connectionTimeout = 25 * time.Second

// TestConnection runs an Ansible ping against a single host in an
// isolated 0700 workspace. Credentials are materialized exactly as for a
// deployment and the workspace is always removed afterwards.
func (b *AnsibleBackend) TestConnection(ctx context.Context, host *ir.Host, secrets SecretResolver) ConnectionResult {
	start := time.Now()
	res := ConnectionResult{Host: host.Name, Address: host.Address}
	if res.Address == "" {
		res.Address = host.Name
	}
	if b.inst == nil || b.runner == nil {
		res.Message = "Ansible is not available on this machine."
		return res
	}
	if strings.TrimSpace(host.Name) == "" {
		res.Message = "This machine has no name."
		return res
	}

	ws, err := os.MkdirTemp("", "visualible-test-*")
	if err != nil {
		res.Message = "Could not create a temporary workspace."
		res.Details = err.Error()
		return res
	}
	defer os.RemoveAll(ws)
	_ = os.Chmod(ws, 0o700)

	// One-host inventory with credentials materialized the same way a
	// deployment does it.
	inv := &ir.Inventory{ID: "test", Name: "test", Hosts: []*ir.Host{host}}
	materialized, err := b.materializeInventory(ctx, inv, secrets, ws)
	if err != nil {
		res.Message = userFacingConnectionError(err.Error())
		res.Details = err.Error()
		res.DurationMS = time.Since(start).Milliseconds()
		return res
	}
	invYAML, err := render.Inventory(materialized)
	if err != nil {
		res.Message = "Could not prepare the connection test."
		res.Details = err.Error()
		return res
	}
	if err := writeFile(ws, "inventory.yml", invYAML, 0o600); err != nil {
		res.Message = "Could not prepare the connection test."
		res.Details = err.Error()
		return res
	}
	cfg := "[defaults]\nretry_files_enabled = False\nhost_key_checking = True\n"
	if err := writeFile(ws, "ansible.cfg", []byte(cfg), 0o600); err != nil {
		res.Message = "Could not prepare the connection test."
		res.Details = err.Error()
		return res
	}

	ctx, cancel := context.WithTimeout(ctx, connectionTimeout)
	defer cancel()

	var out strings.Builder
	code, runErr := b.runner.Run(ctx, RunOpts{
		Path: b.inst.Path,
		Args: []string{"-i", "inventory.yml", "-m", "ping", "all"},
		Dir:  ws,
		Env: []string{
			"ANSIBLE_CONFIG=" + filepath.Join(ws, "ansible.cfg"),
			"ANSIBLE_NOCOLOR=1",
		},
		Stdout: &out,
		Stderr: &out,
	})
	res.DurationMS = time.Since(start).Milliseconds()
	res.Details = strings.TrimSpace(out.String())

	switch {
	case runErr != nil:
		res.Message = "Visualible could not run Ansible to test the connection."
		res.Details = runErr.Error() + "\n" + res.Details
	case code == 0 && strings.Contains(res.Details, `"ping": "pong"`):
		res.OK = true
		res.Message = "Connection works."
	case ctx.Err() == context.DeadlineExceeded:
		res.Message = fmt.Sprintf("Visualible could not reach %s within %s.", res.Address, connectionTimeout)
	default:
		res.Message = userFacingConnectionError(res.Details)
	}
	return res
}

// userFacingConnectionError translates raw Ansible/SSH output into plain
// language while keeping the raw text available in Details.
func userFacingConnectionError(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "permission denied"):
		return "The SSH login was refused. Check the SSH user and credential."
	case strings.Contains(lower, "host key verification failed"):
		return "The machine's SSH host key is not trusted yet. Connect once from a terminal to accept it."
	case strings.Contains(lower, "connection refused"):
		return "The machine refused the SSH connection. Is SSH running on the port you configured?"
	case strings.Contains(lower, "no route to host") || strings.Contains(lower, "network is unreachable"):
		return "The machine is not reachable on the network. Check the address."
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return "The connection timed out. Check the address, port and firewall."
	case strings.Contains(lower, "could not resolve hostname") || strings.Contains(lower, "name or service not known"):
		return "The machine name could not be resolved. Check the address."
	case strings.Contains(lower, "unreachable"):
		return "The machine is unreachable over SSH. Check the address, port and credential."
	case raw == "":
		return "The connection test produced no output."
	default:
		return "The connection test failed. Expand the details for the raw message."
	}
}

// --- Preflight ---

// PreflightHost is one machine's connection status before a deployment.
type PreflightHost struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// PreflightResult summarizes readiness across an inventory.
type PreflightResult struct {
	Hosts     []PreflightHost `json:"hosts"`
	Reachable int             `json:"reachable"`
	Total     int             `json:"total"`
}

// TestConnections tests every machine in the inventory concurrently
// (bounded), so the Deploy stage can show honest readiness before
// anything runs.
func (b *AnsibleBackend) TestConnections(ctx context.Context, inventory *ir.Inventory, secrets SecretResolver) PreflightResult {
	hosts := flattenHosts(inventory)
	res := PreflightResult{Total: len(hosts), Hosts: make([]PreflightHost, len(hosts))}
	if len(hosts) == 0 {
		return res
	}
	const workers = 5
	sem := make(chan struct{}, workers)
	done := make(chan int, len(hosts))
	for i, h := range hosts {
		go func(i int, h *ir.Host) {
			sem <- struct{}{}
			defer func() { <-sem }()
			cr := b.TestConnection(ctx, h, secrets)
			addr := h.Address
			if addr == "" {
				addr = h.Name
			}
			if h.SSHPort > 0 {
				addr = fmt.Sprintf("%s:%d", addr, h.SSHPort)
			}
			res.Hosts[i] = PreflightHost{Name: h.Name, Address: addr, OK: cr.OK, Message: cr.Message}
			done <- i
		}(i, h)
	}
	for range hosts {
		<-done
	}
	for _, h := range res.Hosts {
		if h.OK {
			res.Reachable++
		}
	}
	return res
}

func flattenHosts(inv *ir.Inventory) []*ir.Host {
	if inv == nil {
		return nil
	}
	out := append([]*ir.Host{}, inv.Hosts...)
	var walk func(g *ir.InventoryGroup)
	walk = func(g *ir.InventoryGroup) {
		out = append(out, g.Hosts...)
		for _, c := range g.Children {
			walk(c)
		}
	}
	for _, g := range inv.Groups {
		walk(g)
	}
	return out
}
