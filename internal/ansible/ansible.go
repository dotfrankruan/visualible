// Package ansible integrates with the locally installed Ansible tooling.
// The local installation is the single authority for module/plugin
// metadata: Visualible never ships a hardcoded module database. All data
// comes from ansible-doc, executed with structured arguments (never a
// shell) and normalized into internal schemas.
package ansible

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Installation describes a detected local Ansible installation.
type Installation struct {
	// Path is the ansible CLI executable.
	Path string `json:"path"`
	// AnsibleDocPath is the ansible-doc executable.
	AnsibleDocPath string `json:"ansibleDocPath"`
	// PlaybookPath is the ansible-playbook executable.
	PlaybookPath string `json:"playbookPath"`
	// Version is the detected ansible-core version, e.g. "2.16.3".
	Version string `json:"version"`
}

// ErrNotFound indicates Ansible executables were not found on PATH.
var ErrNotFound = errors.New("ansible not found on PATH")

// Default timeout for ansible-doc invocations; it can be slow on first run.
const docTimeout = 2 * time.Minute

var versionRe = regexp.MustCompile(`(?:ansible|core)\s+\[?(\d+\.\d+(?:\.\d+)?)\]?`)

// Detect locates Ansible executables and determines the version. Returns
// ErrNotFound (wrapped) when ansible is unavailable.
func Detect(ctx context.Context) (*Installation, error) {
	return DetectWith(ctx, exec.LookPath, defaultOutput)
}

// LookPathFunc abstracts executable lookup for testing.
type LookPathFunc func(file string) (string, error)

// OutputFunc runs a command and returns its combined stdout.
type OutputFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func defaultOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

// DetectWith is Detect with injectable lookup/exec for tests.
func DetectWith(ctx context.Context, lookPath LookPathFunc, output OutputFunc) (*Installation, error) {
	ansiblePath, err := lookPath("ansible")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	docPath, err := lookPath("ansible-doc")
	if err != nil {
		return nil, fmt.Errorf("%w: ansible-doc: %v", ErrNotFound, err)
	}
	playbookPath, err := lookPath("ansible-playbook")
	if err != nil {
		return nil, fmt.Errorf("%w: ansible-playbook: %v", ErrNotFound, err)
	}
	inst := &Installation{
		Path:           ansiblePath,
		AnsibleDocPath: docPath,
		PlaybookPath:   playbookPath,
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := output(vctx, ansiblePath, "--version")
	if err == nil {
		inst.Version = parseVersion(string(out))
	}
	return inst, nil
}

func parseVersion(output string) string {
	// Typical first lines:
	//   ansible [core 2.16.3]
	//   ansible [core 2.15.12]  config file = ...
	line, _, _ := strings.Cut(output, "\n")
	if m := versionRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// DocClient retrieves module/plugin metadata from ansible-doc. It is safe
// for concurrent use; each call spawns a fresh process with a timeout.
type DocClient struct {
	docPath string
	output  OutputFunc
	timeout time.Duration
}

// NewDocClient builds a client for the given installation.
func NewDocClient(inst *Installation) *DocClient {
	return &DocClient{
		docPath: inst.AnsibleDocPath,
		output:  defaultOutput,
		timeout: docTimeout,
	}
}

// NewDocClientWith builds a client with an injected executor for tests.
func NewDocClientWith(docPath string, output OutputFunc) *DocClient {
	return &DocClient{docPath: docPath, output: output, timeout: docTimeout}
}

// run executes ansible-doc with structured arguments (never a shell) and
// returns stdout.
func (c *DocClient) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out, err := c.output(ctx, c.docPath, args...)
	if err != nil {
		return nil, fmt.Errorf("ansible-doc %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// ListJSON returns the raw JSON document for `ansible-doc -l -j`,
// mapping FQCN to a summary entry. Callers use NormalizeModuleList.
func (c *DocClient) ListJSON(ctx context.Context) ([]byte, error) {
	return c.run(ctx, "-l", "-j")
}

// DocJSON returns the raw JSON document for `ansible-doc -j <fqcn>`.
func (c *DocClient) DocJSON(ctx context.Context, fqcn string) ([]byte, error) {
	if err := ValidateFQCN(fqcn); err != nil {
		return nil, err
	}
	return c.run(ctx, "-j", fqcn)
}

var fqcnRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// ValidateFQCN guards against option injection: an FQCN that does not look
// like one could be interpreted as a flag by ansible-doc.
func ValidateFQCN(fqcn string) error {
	if !fqcnRe.MatchString(fqcn) {
		return fmt.Errorf("invalid module name %q", fqcn)
	}
	return nil
}
