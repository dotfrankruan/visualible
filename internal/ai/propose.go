package ai

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dotfrankruan/visualible/internal/ir"
)

// proposeSystemPrompt is owned by Visualible. Users never see it; they
// supply only their intent. It constrains the model to return structured
// Visualible IR as JSON — never YAML, never prose, never a deployment.
const proposeSystemPrompt = `You are the automation editing engine inside Visualible, a visual IDE for Ansible.

You receive the user's desired change and, when a project exists, the current automation as Visualible IR (JSON). You respond with the COMPLETE resulting automation as Visualible IR.

OUTPUT CONTRACT — strict:
- Output ONLY one JSON object. No markdown fences, no prose, no explanations, no YAML.
- Shape:
  {"name": string,
   "plays": [{
     "id": string, "name": string, "hosts": string,
     "become": boolean,
     "tasks": [Task], "handlers": [Task]
   }]}
- Task shape (omit empty fields):
  {"id": string, "name": string, "module": string, "args": object,
   "when": string, "notify": [string], "tags": [string],
   "become": boolean, "register": string, "delegate_to": string,
   "changed_when": string, "failed_when": string}

EDITING RULES:
- "module" is always a fully-qualified Ansible module name, e.g. ansible.builtin.apt, ansible.builtin.template, ansible.builtin.systemd_service.
- Prefer purpose-built modules over ansible.builtin.command/shell.
- Give every task a short human-readable name describing intent ("Install nginx", not "apt").
- Use handlers + notify for restart-on-change behavior.
- PRESERVE the "id" of every existing object that remains conceptually the same. Generate NEW ids (short unique strings like "t-7f3a") only for new objects.
- Preserve unrelated existing automation exactly. Modify only what the request requires. Never delete tasks unless the request clearly asks for removal.
- Never include secrets, private keys, or passwords in args.
- Never initiate deployment: you only propose desired state.
- Administrative system changes (packages, services, users, files in system paths) need "become": true on the task or play.`

// ProposeIR asks the provider for the complete proposed playbook IR for
// the given intent, grounded by the current IR (if any). The model
// returns desired state only; Visualible validates, diffs and merges —
// the model is never authoritative about what changed.
func ProposeIR(ctx context.Context, p Provider, intent string, current *ir.Playbook) (*ir.Playbook, error) {
	if strings.TrimSpace(intent) == "" {
		return nil, fmt.Errorf("intent must not be empty")
	}
	var user strings.Builder
	if current != nil && len(current.Plays) > 0 {
		data, err := json.Marshal(current)
		if err != nil {
			return nil, err
		}
		user.WriteString("Current automation (Visualible IR):\n")
		user.Write(data)
		user.WriteString("\n\n")
	}
	user.WriteString("Requested change:\n")
	user.WriteString(intent)
	user.WriteString("\n\nRespond with the complete resulting Visualible IR as one JSON object.")

	raw, err := p.Complete(ctx, []Message{
		{Role: "system", Content: proposeSystemPrompt},
		{Role: "user", Content: user.String()},
	})
	if err != nil {
		return nil, err
	}
	proposed, err := ParseIR(raw)
	if err != nil {
		return nil, err
	}
	return proposed, nil
}

// ParseIR parses and normalizes a model response into a playbook. It is
// deliberately strict about structure but tolerant of fence/prose noise
// around the JSON object. Missing IDs are assigned; names are defaulted.
func ParseIR(raw string) (*ir.Playbook, error) {
	text := extractJSON(raw)
	if text == "" {
		return nil, fmt.Errorf("AI output contained no JSON object")
	}
	var pb ir.Playbook
	if err := json.Unmarshal([]byte(text), &pb); err != nil {
		return nil, fmt.Errorf("AI output was not valid Visualible IR: %w", err)
	}
	if len(pb.Plays) == 0 {
		return nil, fmt.Errorf("AI output contained no plays")
	}
	normalizeIDs(&pb)
	return &pb, nil
}

// extractJSON finds the first balanced {...} region, tolerating markdown
// fences and prose around it.
func extractJSON(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			s = strings.TrimSpace(rest[:j])
		}
	}
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if esc {
			esc = false
			continue
		}
		if c == '\\' && inStr {
			esc = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// normalizeIDs ensures every object has a stable ID and every task a
// name, so the diff engine can match against the base IR.
func normalizeIDs(pb *ir.Playbook) {
	if pb.ID == "" {
		pb.ID = newID("pb")
	}
	if pb.Name == "" {
		pb.Name = "playbook"
	}
	seen := map[string]bool{}
	fix := func(prefix, id string) string {
		if id == "" || seen[id] {
			id = newID(prefix)
		}
		seen[id] = true
		return id
	}
	for _, play := range pb.Plays {
		play.ID = fix("play", play.ID)
		if play.Name == "" {
			play.Name = play.Hosts
		}
		if play.Hosts == "" {
			play.Hosts = "all"
		}
		lists := [][]*ir.Task{play.PreTasks, play.Tasks, play.PostTasks, play.Handlers}
		for _, list := range lists {
			for _, t := range list {
				t.ID = fix("t", t.ID)
				if t.Name == "" {
					t.Name = t.Module
				}
			}
		}
	}
}

func newID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%x", prefix, b)
}
