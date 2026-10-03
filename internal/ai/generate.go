package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/visualible/visualible/internal/parse"
)

const generateSystemPrompt = `You generate Ansible playbooks for the Visualible IDE.

Rules:
- Output ONLY Ansible playbook YAML: a YAML list of plays. No prose, no markdown fences, no explanations.
- Use fully-qualified module names (e.g. ansible.builtin.apt, ansible.builtin.template, ansible.builtin.systemd_service).
- Prefer purpose-built Ansible modules over ansible.builtin.shell/command.
- Make tasks idempotent. Give every task a human-readable name.
- Use handlers for service restarts triggered by configuration changes (notify).
- Do not invent inventory hosts; use the host pattern the user provides, or "all" if unspecified.
- Do not include vault-encrypted values, API keys, passwords, or any secrets in the output.`

// GeneratePlaybook asks the provider for a playbook implementing intent,
// then parses the result into Visualible IR. The IR is the deliverable —
// never raw YAML — so proposals are validated, diagnosable, and editable
// before a human decides to apply them. currentYAML, when non-empty,
// grounds "extend this playbook" requests.
func GeneratePlaybook(ctx context.Context, p Provider, intent, currentYAML string) (*parse.Result, string, error) {
	if strings.TrimSpace(intent) == "" {
		return nil, "", fmt.Errorf("intent must not be empty")
	}
	var user strings.Builder
	user.WriteString("Create an Ansible playbook for the following requirement:\n\n")
	user.WriteString(intent)
	if strings.TrimSpace(currentYAML) != "" {
		user.WriteString("\n\nExtend/adapt this existing playbook rather than starting from scratch:\n\n")
		user.WriteString(currentYAML)
	}
	raw, err := p.Complete(ctx, []Message{
		{Role: "system", Content: generateSystemPrompt},
		{Role: "user", Content: user.String()},
	})
	if err != nil {
		return nil, "", err
	}
	yamlText := ExtractYAML(raw)
	res, err := parse.Playbook("ai-proposal", []byte(yamlText))
	if err != nil {
		return nil, raw, fmt.Errorf("AI output was not a parseable playbook: %w", err)
	}
	return res, raw, nil
}

// ExtractYAML pulls the playbook out of a model response, tolerating
// markdown fences and stray prose around a fenced block.
func ExtractYAML(raw string) string {
	s := strings.TrimSpace(raw)
	// Fenced block takes precedence if present.
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	// Otherwise: from the first line that looks like a play start.
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "- name:") || strings.HasPrefix(t, "- hosts:") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return s
}
