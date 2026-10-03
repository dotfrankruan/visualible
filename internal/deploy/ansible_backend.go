package deploy

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dotfrankruan/visualible/internal/ansible"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/render"
)

//go:embed callback/visualible_events.py
var callbackPlugin []byte

// AnsibleBackend executes deployments with ansible-playbook over SSH.
// Visualible is the control node; managed nodes need no agent.
type AnsibleBackend struct {
	inst   *ansible.Installation
	runner Runner

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewAnsibleBackend builds the backend for a detected installation.
// inst may be nil (Validate/Prepare will then report unavailability).
func NewAnsibleBackend(inst *ansible.Installation) *AnsibleBackend {
	return &AnsibleBackend{inst: inst, runner: ExecRunner{}, running: map[string]context.CancelFunc{}}
}

// NewAnsibleBackendWith injects a runner for tests.
func NewAnsibleBackendWith(inst *ansible.Installation, runner Runner) *AnsibleBackend {
	return &AnsibleBackend{inst: inst, runner: runner, running: map[string]context.CancelFunc{}}
}

func (b *AnsibleBackend) ID() string { return "ansible-ssh" }

func (b *AnsibleBackend) Metadata() BackendMetadata {
	return BackendMetadata{
		ID:          b.ID(),
		Name:        "Ansible over SSH",
		Description: "Runs ansible-playbook from this machine; managed nodes need no agent.",
	}
}

func (b *AnsibleBackend) Capabilities() Capabilities {
	return Capabilities{
		SupportsFleet:        true,
		SupportsStreaming:    true,
		SupportsCheckMode:    true,
		SupportsDiff:         true,
		SupportsCancellation: true,
		RequiresInboundSSH:   true,
	}
}

// --- Runner abstracts process execution for tests ---

// Runner executes a child process. Implementations must stream stdout and
// stderr to the provided writers and honor context cancellation by
// killing the whole process group.
type Runner interface {
	Run(ctx context.Context, opts RunOpts) (exitCode int, err error)
}

// RunOpts describes one child process invocation. Args are always
// structured — no shell is ever involved.
type RunOpts struct {
	Path   string
	Args   []string
	Dir    string
	Env    []string
	Stdout io.Writer
	Stderr io.Writer
}

// ExecRunner is the production Runner.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, opts RunOpts) (int, error) {
	cmd := exec.CommandContext(ctx, opts.Path, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	// New process group so cancellation can kill ansible *and* its
	// children (ssh connections, local actions).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start %s: %w", filepath.Base(opts.Path), err)
	}
	// On cancellation, kill the process group, not just the parent.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()
	err := cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

// --- Validation ---

func (b *AnsibleBackend) findProject(project *ir.Project, plan *ir.DeploymentPlan) (*ir.Playbook, *ir.Inventory, error) {
	var pb *ir.Playbook
	for _, x := range project.Playbooks {
		if x.ID == plan.PlaybookID {
			pb = x
		}
	}
	if pb == nil {
		return nil, nil, fmt.Errorf("playbook %q not found in project", plan.PlaybookID)
	}
	var inv *ir.Inventory
	for _, x := range project.Inventories {
		if x.ID == plan.InventoryID {
			inv = x
		}
	}
	if inv == nil {
		return nil, nil, fmt.Errorf("inventory %q not found in project", plan.InventoryID)
	}
	return pb, inv, nil
}

func (b *AnsibleBackend) Validate(ctx context.Context, plan *ir.DeploymentPlan, project *ir.Project) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if b.inst == nil {
		return fmt.Errorf("ansible is not available on this machine")
	}
	pb, inv, err := b.findProject(project, plan)
	if err != nil {
		return err
	}
	if err := pb.Validate(); err != nil {
		return fmt.Errorf("playbook is not deployable: %w", err)
	}
	if err := inv.Validate(); err != nil {
		return fmt.Errorf("inventory is not deployable: %w", err)
	}
	if countHosts(inv) == 0 {
		return fmt.Errorf("inventory %q has no hosts", inv.Name)
	}
	return nil
}

func countHosts(inv *ir.Inventory) int {
	n := len(inv.Hosts)
	var walk func(g *ir.InventoryGroup)
	walk = func(g *ir.InventoryGroup) {
		n += len(g.Hosts)
		for _, c := range g.Children {
			walk(c)
		}
	}
	for _, g := range inv.Groups {
		walk(g)
	}
	return n
}

// --- Prepare ---

type preparedData struct {
	EventFile string
	Args      []string
	Env       []string
}

func (b *AnsibleBackend) Prepare(ctx context.Context, plan *ir.DeploymentPlan, project *ir.Project, secrets SecretResolver) (*PreparedDeployment, error) {
	if err := b.Validate(ctx, plan, project); err != nil {
		return nil, err
	}
	pb, inv, _ := b.findProject(project, plan)

	ws, err := os.MkdirTemp("", "visualible-deploy-*")
	if err != nil {
		return nil, err
	}
	prepared := &PreparedDeployment{ID: plan.ID, Plan: plan, Workspace: ws}
	cleanupOnErr := func(err error) (*PreparedDeployment, error) {
		_ = b.Cleanup(ctx, prepared)
		return nil, err
	}
	if err := os.Chmod(ws, 0o700); err != nil {
		return cleanupOnErr(err)
	}

	// 1. Render playbook.
	pbYAML, err := render.Playbook(pb)
	if err != nil {
		return cleanupOnErr(fmt.Errorf("render playbook: %w", err))
	}
	if err := writeFile(ws, "playbook.yml", pbYAML, 0o600); err != nil {
		return cleanupOnErr(err)
	}

	// 2. Materialize credentials into an augmented inventory clone.
	invClone, err := b.materializeInventory(ctx, inv, secrets, ws)
	if err != nil {
		return cleanupOnErr(err)
	}
	invYAML, err := render.Inventory(invClone)
	if err != nil {
		return cleanupOnErr(fmt.Errorf("render inventory: %w", err))
	}
	if err := writeFile(ws, "inventory.yml", invYAML, 0o600); err != nil {
		return cleanupOnErr(err)
	}

	// 3. Event callback plugin + config.
	if err := os.MkdirAll(filepath.Join(ws, "callback_plugins"), 0o700); err != nil {
		return cleanupOnErr(err)
	}
	if err := writeFile(filepath.Join(ws, "callback_plugins"), "visualible_events.py", callbackPlugin, 0o600); err != nil {
		return cleanupOnErr(err)
	}
	cfg := "[defaults]\n" +
		"retry_files_enabled = False\n" +
		"stdout_callback = default\n" +
		"callbacks_enabled = visualible_events\n" +
		"display_skipped_hosts = False\n"
	if err := writeFile(ws, "ansible.cfg", []byte(cfg), 0o600); err != nil {
		return cleanupOnErr(err)
	}
	eventFile := filepath.Join(ws, "events.jsonl")
	if err := writeFile(ws, "events.jsonl", nil, 0o600); err != nil {
		return cleanupOnErr(err)
	}

	// 4. Preflight: syntax check (structured args, never a shell).
	syntaxArgs := []string{"-i", "inventory.yml", "playbook.yml", "--syntax-check"}
	if code, err := b.runner.Run(ctx, RunOpts{
		Path: b.inst.PlaybookPath, Args: syntaxArgs, Dir: ws,
		Env: workspaceEnv(ws, eventFile),
	}); err != nil {
		return cleanupOnErr(fmt.Errorf("preflight: run ansible-playbook: %w", err))
	} else if code != 0 {
		return cleanupOnErr(fmt.Errorf("preflight: ansible-playbook --syntax-check failed (exit %d)", code))
	}

	// 5. Build the execution invocation.
	args := []string{"-i", "inventory.yml", "playbook.yml"}
	if plan.Check {
		args = append(args, "--check")
	}
	if plan.Diff {
		args = append(args, "--diff")
	}
	if len(plan.Tags) > 0 {
		args = append(args, "--tags", strings.Join(plan.Tags, ","))
	}
	if plan.Limit != "" {
		args = append(args, "--limit", plan.Limit)
	}
	if plan.Verbosity > 0 {
		args = append(args, "-"+strings.Repeat("v", plan.Verbosity))
	}
	prepared.BackendData = &preparedData{
		EventFile: eventFile,
		Args:      args,
		Env:       workspaceEnv(ws, eventFile),
	}
	return prepared, nil
}

func workspaceEnv(ws, eventFile string) []string {
	return []string{
		"ANSIBLE_CONFIG=" + filepath.Join(ws, "ansible.cfg"),
		"ANSIBLE_CALLBACK_PLUGINS=" + filepath.Join(ws, "callback_plugins"),
		"ANSIBLE_CALLBACKS_ENABLED=visualible_events",
		"VISUALIBLE_EVENT_FILE=" + eventFile,
		"ANSIBLE_NOCOLOR=1",
	}
}

// materializeInventory deep-copies the inventory and injects connection
// secrets as host vars. Key material goes to 0600 files; passwords go
// into the ephemeral workspace inventory (0700 dir, removed on cleanup)
// — never into the project's stored inventory.
func (b *AnsibleBackend) materializeInventory(ctx context.Context, inv *ir.Inventory, secrets SecretResolver, ws string) (*ir.Inventory, error) {
	clone := cloneInventory(inv)
	hosts := map[string]*ir.Host{}
	collectHosts(clone.Hosts, hosts)
	for _, g := range clone.Groups {
		collectGroupHosts(g, hosts)
	}
	for _, h := range hosts {
		if h.CredentialID == "" {
			continue
		}
		kind, secret, err := secrets.ResolveSecret(ctx, h.CredentialID)
		if err != nil {
			return nil, fmt.Errorf("resolve credential for host %q: %w", h.Name, err)
		}
		if h.Vars == nil {
			h.Vars = map[string]any{}
		}
		switch kind {
		case "ssh_key":
			keyDir := filepath.Join(ws, "keys")
			if err := os.MkdirAll(keyDir, 0o700); err != nil {
				return nil, err
			}
			keyFile := filepath.Join(keyDir, sanitizeFileName(h.Name)+".key")
			if err := os.WriteFile(keyFile, secret, 0o600); err != nil {
				return nil, err
			}
			h.Vars["ansible_ssh_private_key_file"] = keyFile
		case "ssh_password":
			h.Vars["ansible_password"] = string(secret)
		case "become_password":
			h.Vars["ansible_become_password"] = string(secret)
		default:
			return nil, fmt.Errorf("credential kind %q cannot be used for SSH connections (host %q)", kind, h.Name)
		}
	}
	return clone, nil
}

var fileNameSanitizer = strings.NewReplacer("/", "_", "\\", "_", "..", "_", " ", "_")

func sanitizeFileName(s string) string {
	return fileNameSanitizer.Replace(s)
}

func cloneInventory(inv *ir.Inventory) *ir.Inventory {
	c := &ir.Inventory{ID: inv.ID, Name: inv.Name}
	c.Hosts = cloneHosts(inv.Hosts)
	for _, g := range inv.Groups {
		c.Groups = append(c.Groups, cloneGroup(g))
	}
	return c
}

func cloneGroup(g *ir.InventoryGroup) *ir.InventoryGroup {
	c := &ir.InventoryGroup{ID: g.ID, Name: g.Name, Vars: cloneMap(g.Vars)}
	c.Hosts = cloneHosts(g.Hosts)
	for _, ch := range g.Children {
		c.Children = append(c.Children, cloneGroup(ch))
	}
	return c
}

func cloneHosts(hosts []*ir.Host) []*ir.Host {
	out := make([]*ir.Host, 0, len(hosts))
	for _, h := range hosts {
		cp := *h
		cp.Vars = cloneMap(h.Vars)
		out = append(out, &cp)
	}
	return out
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func collectHosts(hosts []*ir.Host, into map[string]*ir.Host) {
	for _, h := range hosts {
		into[h.ID] = h
	}
}

func collectGroupHosts(g *ir.InventoryGroup, into map[string]*ir.Host) {
	collectHosts(g.Hosts, into)
	for _, c := range g.Children {
		collectGroupHosts(c, into)
	}
}

func writeFile(dir, name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(filepath.Join(dir, name), data, perm)
}

// --- Execute ---

func (b *AnsibleBackend) Execute(ctx context.Context, prepared *PreparedDeployment, sink EventSink) (int, error) {
	data, ok := prepared.BackendData.(*preparedData)
	if !ok {
		return -1, fmt.Errorf("prepared deployment was not created by this backend")
	}
	ctx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.running[prepared.ID] = cancel
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.running, prepared.ID)
		b.mu.Unlock()
		cancel()
	}()

	// Tail the event file until the process exits.
	stopTail := make(chan struct{})
	tailDone := make(chan struct{})
	go func() {
		defer close(tailDone)
		tailEvents(ctx, data.EventFile, prepared.ID, sink, stopTail)
	}()

	lineSink := &lineWriter{emit: func(stream string, line string) {
		ev := NewEvent(prepared.ID, ir.EventLogStdout)
		if stream == "stderr" {
			ev.Type = ir.EventLogStderr
		}
		ev.Message = line
		sink.Emit(ev)
	}}

	exitCode, err := b.runner.Run(ctx, RunOpts{
		Path:   b.inst.PlaybookPath,
		Args:   data.Args,
		Dir:    prepared.Workspace,
		Env:    data.Env,
		Stdout: lineSink.stream("stdout"),
		Stderr: lineSink.stream("stderr"),
	})

	close(stopTail)
	<-tailDone
	return exitCode, err
}

// tailEvents reads appended JSON lines from the callback event file and
// emits normalized events until stop is closed, then drains once more.
func tailEvents(ctx context.Context, path, deploymentID string, sink EventSink, stop chan struct{}) {
	var offset int64
	readNew := func() {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return
		}
		buf, err := io.ReadAll(f)
		if err != nil {
			return
		}
		offset += int64(len(buf))
		for _, line := range strings.Split(string(buf), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if ev := NormalizeCallbackEvent(deploymentID, []byte(line)); ev != nil {
				sink.Emit(*ev)
			}
		}
	}
	for {
		select {
		case <-stop:
			readNew() // final drain
			return
		case <-ctx.Done():
			return
		default:
			readNew()
			select {
			case <-stop:
				readNew()
				return
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
}

// lineWriter splits process output into lines and forwards them.
type lineWriter struct {
	emit func(stream, line string)
	bufs map[string][]byte
}

func (l *lineWriter) stream(name string) io.Writer {
	if l.bufs == nil {
		l.bufs = map[string][]byte{}
	}
	return &streamWriter{name: name, parent: l}
}

type streamWriter struct {
	name   string
	parent *lineWriter
}

func (w *streamWriter) Write(p []byte) (int, error) {
	buf := append(w.parent.bufs[w.name], p...)
	for {
		i := strings.IndexByte(string(buf), '\n')
		if i < 0 {
			break
		}
		w.parent.emit(w.name, string(buf[:i]))
		buf = buf[i+1:]
	}
	w.parent.bufs[w.name] = buf
	return len(p), nil
}

// --- Cancel / Cleanup ---

func (b *AnsibleBackend) Cancel(ctx context.Context, deploymentID string) error {
	b.mu.Lock()
	cancel, ok := b.running[deploymentID]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("deployment %q is not running", deploymentID)
	}
	cancel()
	return nil
}

func (b *AnsibleBackend) Cleanup(ctx context.Context, prepared *PreparedDeployment) error {
	if prepared.Workspace == "" {
		return nil
	}
	// Restrict to our own temp workspaces before deleting.
	if !strings.Contains(filepath.Base(prepared.Workspace), "visualible-deploy-") {
		return fmt.Errorf("refusing to clean up unexpected workspace %q", prepared.Workspace)
	}
	return os.RemoveAll(prepared.Workspace)
}
