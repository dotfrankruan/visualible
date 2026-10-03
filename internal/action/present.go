package action

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dotfrankruan/visualible/internal/ir"
)

// This file is Visualible's presentation layer for automation tasks: it
// translates module/argument semantics into machine-level intent for
// people who do not know Ansible.
//
// It is display-only. Nothing here changes the IR, and when a task cannot
// be translated confidently the result is an explicit "Advanced Ansible
// task" with its real module and arguments — raw truth beats a confident
// sounding falsehood.

// Presentation is the beginner-facing description of one task.
type Presentation struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Module   string   `json:"module"`
	Details  []Detail `json:"details,omitempty"`
	Risks    []string `json:"risks,omitempty"`
	// Advanced marks a task shown as technical because it cannot be
	// represented faithfully in simple language.
	Advanced bool `json:"advanced,omitempty"`
	// Handler marks an automation reaction (Ansible handler).
	Handler bool `json:"handler,omitempty"`
}

// Detail is one labelled fact about an automation step ("Destination →
// localhost:8888"). Lists and large values get their own shapes so the UI
// can render them without dumping paragraphs into a card.
type Detail struct {
	Label  string     `json:"label"`
	Value  string     `json:"value,omitempty"`
	Values []string   `json:"values,omitempty"`
	Long   *LongValue `json:"long,omitempty"`
}

// LongValue summarizes an oversized argument instead of inlining it.
type LongValue struct {
	Lines   int    `json:"lines"`
	Chars   int    `json:"chars"`
	Preview string `json:"preview"`
}

// longValueThreshold is the character count above which a value is
// summarized rather than shown inline.
const longValueThreshold = 160

// Present describes a task for display. isHandler marks tasks that only
// run when something changes (Ansible handlers), which are presented as
// reactions rather than as steps.
func Present(t *ir.Task, isHandler bool) Presentation {
	rec := RecognizeOne(t)
	p := rec.Presentation
	p.Handler = isHandler
	if isHandler {
		p.Title, p.Details = presentHandler(rec, t)
		return p
	}
	// A human-written task name ("Disable default site") describes intent
	// better than the derived label ("Disable default"), so prefer it when
	// it actually reads like a sentence. Auto-generated names that just
	// repeat the module fall back to the curated label.
	if rec.Recognized && humanReadableName(t.Name, t.Module) {
		p.Title = t.Name
	}
	p.Details = append(p.Details, notifyDetails(t)...)
	if d := whenDetail(t.When); d != nil {
		p.Details = append(p.Details, *d)
	}
	return p
}

// whenDetail explains a simple condition in plain language. Conditions we
// cannot paraphrase are shown verbatim rather than summarised wrongly —
// the point is that a beginner learns *why* a step may be skipped.
func whenDetail(when string) *Detail {
	expr := strings.TrimSpace(when)
	if expr == "" {
		return nil
	}
	simple := map[string]string{
		"ansible_os_family == 'Debian'":    "Only on Debian-family machines",
		"ansible_os_family == \"Debian\"":  "Only on Debian-family machines",
		"ansible_os_family == 'RedHat'":    "Only on Red Hat-family machines",
		"ansible_os_family == \"RedHat\"":  "Only on Red Hat-family machines",
		"ansible_os_family == 'Suse'":      "Only on SUSE-family machines",
		"ansible_os_family == 'Archlinux'": "Only on Arch-family machines",
	}
	if msg, ok := simple[expr]; ok {
		return &Detail{Label: "Runs", Value: msg}
	}
	if strings.HasPrefix(expr, "ansible_distribution ==") {
		name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(expr, "ansible_distribution ==")), "'\"")
		if name != "" {
			return &Detail{Label: "Runs", Value: "Only on " + name + " machines"}
		}
	}
	return &Detail{Label: "Only when", Value: expr}
}

// notifyDetails presents "what happens when this changes" so a beginner
// sees the reaction without learning about Ansible handlers. The raw
// handler semantics stay visible in the technical-details expander.
func notifyDetails(t *ir.Task) []Detail {
	if len(t.Notify) == 0 {
		return nil
	}
	return []Detail{{Label: "When this changes", Values: t.Notify}}
}

// humanReadableName reports whether a task name reads like intent rather
// than a module identifier.
func humanReadableName(name, module string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == module {
		return false
	}
	lower := strings.ToLower(name)
	if strings.Contains(lower, "ansible.") || strings.Contains(lower, module) {
		return false
	}
	// Phrases carry intent; single tokens ("package") do not.
	if !strings.Contains(name, " ") {
		return false
	}
	for _, bad := range []string{"state=", "=true", "=false", "_"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	return true
}

// presentHandler reframes a handler as a reaction: "Restart nginx when
// configuration changes".
func presentHandler(rec Recognition, t *ir.Task) (string, []Detail) {
	svc := ""
	if rec.Recognized {
		if s := str(rec.Params, "service"); s != "" {
			svc = s
		}
	}
	verb := "Update"
	if rec.Recognized {
		if state := str(rec.Params, "state"); state != "" {
			verb = map[string]string{
				"restarted": "Restart", "reloaded": "Reload", "started": "Start", "stopped": "Stop",
			}[state]
			if verb == "" {
				verb = "Update"
			}
		}
	}
	if svc == "" {
		// Unknown handler shape: name it honestly.
		name := t.Name
		if name == "" {
			name = t.Module
		}
		return name + " when configuration changes", []Detail{
			{Label: "Triggered by", Value: "configuration changes"},
			{Label: "Ansible handler", Value: t.Module},
		}
	}
	title := fmt.Sprintf("%s %s when configuration changes", verb, svc)
	return title, []Detail{
		{Label: "Triggered by", Value: svc + " configuration changes"},
		{Label: "Runs when", Value: fmt.Sprintf("a step notifies this (%s)", svc)},
	}
}

// describeDetails returns the labelled facts for a recognized Action.
// Every branch here is deterministic knowledge about what the module
// arguments mean, not a guess.
func describeDetails(actionID string, params map[string]any) []Detail {
	switch actionID {
	case "install-software":
		state := str(params, "state")
		if state == "" {
			state = "present"
		}
		details := []Detail{
			{Label: "Package", Value: str(params, "package")},
			{Label: "Ensure", Value: stateLabel("", state)},
		}
		if svc := str(params, "serviceName"); svc != "" {
			was := []string{}
			if boolParam(params, "ensureRunning") {
				was = append(was, "running")
			}
			if boolParam(params, "enableBoot") {
				was = append(was, "starts automatically at boot")
			}
			if len(was) > 0 {
				details = append(details, Detail{Label: "Its service", Value: svc + " — " + strings.Join(was, ", ")})
			}
		}
		return details

	case "manage-service":
		state := str(params, "state")
		details := []Detail{
			{Label: "Service", Value: str(params, "service")},
			{Label: "State", Value: stateLabel("", state)},
		}
		if boolParam(params, "enabledAtBoot") {
			details = append(details, Detail{Label: "Start automatically after reboot", Value: "Yes"})
		}
		return details

	case "create-user":
		if str(params, "state") == "absent" {
			return []Detail{{Label: "Removes", Value: "user account " + str(params, "username")}}
		}
		details := []Detail{{Label: "Username", Value: str(params, "username")}}
		if shell := str(params, "shell"); shell != "" {
			details = append(details, Detail{Label: "Shell", Value: shell})
		}
		if boolParam(params, "admin") {
			details = append(details, Detail{Label: "Administrator privileges", Value: "Yes (sudo group)"})
		}
		return details

	case "add-ssh-key":
		key := str(params, "key")
		details := []Detail{{Label: "User", Value: str(params, "username")}}
		if key != "" {
			details = append(details, Detail{Label: "Public key", Value: summarizeKey(key)})
		}
		return details

	case "deploy-file":
		details := []Detail{{Label: "File", Value: str(params, "dest")}}
		if str(params, "source") == "path" {
			details = append(details, Detail{Label: "Copied from", Value: str(params, "sourcePath")})
		} else if content := str(params, "content"); content != "" {
			// Report directives that are literally present in the file, so
			// the effect is visible without reading the content. This
			// extracts known nginx directives; it never guesses.
			details = append(details, nginxHints(content)...)
			details = append(details, detailForContent("Content", content))
		}
		details = append(details, permDetails(params)...)
		return details

	case "render-template":
		details := []Detail{
			{Label: "Configuration file", Value: str(params, "dest")},
			{Label: "Generated from", Value: str(params, "templatePath")},
		}
		// The "When this changes" row is added centrally from the task's
		// notify list, so it always shows the real handler name.
		return append(details, permDetails(params)...)

	case "create-directory":
		details := []Detail{{Label: "Folder", Value: str(params, "path")}}
		details = append(details, permDetails(params)...)
		return details

	case "link-file":
		return []Detail{
			{Label: "Enables", Value: str(params, "dest")},
			{Label: "Points to", Value: str(params, "src")},
		}

	case "remove-path":
		return []Detail{{Label: "Removes", Value: str(params, "path")}}

	case "run-command":
		return []Detail{
			detailForContent("Command", str(params, "command")),
			{Label: "Runs with", Value: chooseText(boolParam(params, "sudo"), "administrator privileges", "the current user")},
		}

	case "docker-container":
		state := str(params, "state")
		details := []Detail{
			{Label: "Container", Value: str(params, "name")},
			{Label: "Image", Value: str(params, "image")},
			{Label: "State", Value: stateLabel("", state)},
		}
		if ports := str(params, "ports"); ports != "" {
			details = append(details, Detail{Label: "Published ports", Values: splitList(ports)})
		}
		if policy := str(params, "restartPolicy"); policy != "" {
			details = append(details, Detail{Label: "Restart policy", Value: policy})
		}
		return details
	}
	return nil
}

// describeRawArgs renders a task's real arguments as structured rows.
// Large values are summarized, never dumped.
func describeRawArgs(t *ir.Task) []Detail {
	keys := make([]string, 0, len(t.Args))
	for k := range t.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var details []Detail
	for _, k := range keys {
		v := t.Args[k]
		switch val := v.(type) {
		case string:
			details = append(details, detailForContent(k, val))
		case []any:
			items := make([]string, 0, len(val))
			for _, item := range val {
				items = append(items, fmt.Sprint(item))
			}
			details = append(details, Detail{Label: k, Values: items})
		default:
			details = append(details, Detail{Label: k, Value: fmt.Sprint(v)})
		}
	}
	if len(t.Notify) > 0 {
		details = append(details, Detail{Label: "notify", Values: t.Notify})
	}
	if t.Become != nil && *t.Become {
		details = append(details, Detail{Label: "Administrator privileges", Value: "Yes"})
	}
	return details
}

// nginxHints extracts the directives that decide where traffic goes,
// when they are literally present in the file content. Deterministic
// pattern extraction only — no inference, and nothing is shown when the
// directives are absent.
func nginxHints(content string) []Detail {
	var out []Detail
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ";"))
		switch {
		case strings.HasPrefix(line, "listen "):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "listen ")); v != "" {
				out = append(out, Detail{Label: "Incoming port", Value: v})
			}
		case strings.HasPrefix(line, "proxy_pass "):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "proxy_pass ")); v != "" {
				out = append(out, Detail{Label: "Forwards traffic to", Value: v})
			}
		case strings.HasPrefix(line, "server_name "):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "server_name ")); v != "" {
				out = append(out, Detail{Label: "Answers for", Value: v})
			}
		}
		if len(out) >= 4 {
			break
		}
	}
	return out
}

// detailForContent summarizes multi-line or oversized values.
func detailForContent(label, value string) Detail {
	lines := strings.Count(value, "\n") + 1
	if len(value) > longValueThreshold || lines > 4 {
		return Detail{Label: label, Long: &LongValue{
			Lines:   lines,
			Chars:   len(value),
			Preview: previewOf(value, 3, 200),
		}}
	}
	return Detail{Label: label, Value: strings.TrimRight(value, "\n")}
}

func previewOf(value string, maxLines, maxChars int) string {
	lines := strings.Split(strings.TrimRight(value, "\n"), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "…")
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxChars {
		out = strings.TrimSpace(out[:maxChars]) + "…"
	}
	return out
}

// summarizeKey shows the key type and comment, never the whole blob.
func summarizeKey(key string) string {
	fields := strings.Fields(strings.TrimSpace(key))
	if len(fields) == 0 {
		return "public key"
	}
	kind := fields[0]
	if len(fields) >= 3 {
		return fmt.Sprintf("%s … %s", kind, fields[len(fields)-1])
	}
	return kind + " …"
}

func permDetails(params map[string]any) []Detail {
	var details []Detail
	if owner := str(params, "owner"); owner != "" {
		details = append(details, Detail{Label: "Owner", Value: owner})
	}
	if group := str(params, "group"); group != "" {
		details = append(details, Detail{Label: "Group", Value: group})
	}
	if mode := str(params, "mode"); mode != "" {
		details = append(details, Detail{Label: "Permissions", Value: mode})
	}
	return details
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func chooseText(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// --- deterministic risk hints ---------------------------------------
//
// Small, explicit, testable rules only. This is not a security engine and
// the AI never assigns risk levels: the hints come from what the task
// itself does.

type riskRule struct {
	modules []string
	match   func(*ir.Task) bool
	message string
}

var riskRules = []riskRule{
	{
		modules: []string{"ansible.builtin.file"},
		match: func(t *ir.Task) bool {
			return argString(t.Args, "state") == "absent"
		},
		message: "Removes an existing file or link",
	},
	{
		modules: []string{"ansible.builtin.user"},
		match: func(t *ir.Task) bool {
			return argString(t.Args, "state") == "absent"
		},
		message: "Deletes a user account",
	},
	{
		modules: []string{"ansible.builtin.user"},
		match: func(t *ir.Task) bool {
			groups := argStringList(t.Args, "groups")
			for _, g := range groups {
				if g == "sudo" || g == "wheel" {
					return true
				}
			}
			return false
		},
		message: "Grants administrator privileges",
	},
	{
		modules: []string{"ansible.posix.authorized_key", "ansible.builtin.authorized_key"},
		match:   func(t *ir.Task) bool { return true },
		message: "Changes SSH authentication",
	},
	{
		modules: []string{"ansible.builtin.package", "ansible.builtin.apt", "ansible.builtin.dnf", "ansible.builtin.yum"},
		match: func(t *ir.Task) bool {
			state := argString(t.Args, "state")
			return state == "absent" || state == "removed"
		},
		message: "Removes installed software",
	},
	{
		modules: []string{"ansible.builtin.service", "ansible.builtin.systemd_service", "ansible.builtin.systemd"},
		match: func(t *ir.Task) bool {
			state := argString(t.Args, "state")
			return state == "stopped" || state == "restarted" || state == "reloaded"
		},
		message: "Interrupts a running service",
	},
	{
		modules: []string{"ansible.builtin.command", "ansible.builtin.shell"},
		match:   func(t *ir.Task) bool { return true },
		message: "Runs a raw command on the machines",
	},
	{
		modules: []string{"community.general.ufw", "ansible.posix.firewalld", "community.general.firewalld",
			"ansible.builtin.iptables", "ansible.builtin.iptables"},
		match:   func(t *ir.Task) bool { return true },
		message: "Changes firewall rules",
	},
	{
		modules: []string{"ansible.builtin.reboot"},
		match:   func(t *ir.Task) bool { return true },
		message: "Reboots the machines",
	},
}

// risksFor returns the deterministic warnings that apply to a task.
func risksFor(t *ir.Task) []string {
	var out []string
	for _, r := range riskRules {
		if !contains(r.modules, t.Module) {
			continue
		}
		if r.match(t) && !contains(out, r.message) {
			out = append(out, r.message)
		}
	}
	return out
}

// Risks is the exported form used by callers that only have a task.
func Risks(t *ir.Task) []string { return risksFor(t) }

// RawArguments renders a task's real module arguments (and task-level
// options) as structured rows for the "Show Ansible details" expander.
// Large values are summarized; nothing is hidden or rewritten.
func RawArguments(t *ir.Task) []Detail {
	if t == nil {
		return nil
	}
	details := describeRawArgs(t)
	if t.When != "" {
		details = append(details, Detail{Label: "when", Value: t.When})
	}
	if len(t.Tags) > 0 {
		details = append(details, Detail{Label: "tags", Values: t.Tags})
	}
	if t.Register != "" {
		details = append(details, Detail{Label: "register", Value: t.Register})
	}
	return details
}
