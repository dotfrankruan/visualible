package present

import (
	"fmt"
	"strings"

	"github.com/dotfrankruan/visualible/internal/action"
	"github.com/dotfrankruan/visualible/internal/aidiff"
)

// Count renders a number with a correctly pluralized noun:
// Count(1, "change") -> "1 change", Count(3, "change") -> "3 changes".
func Count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Headline summarizes a proposal in one line.
func Headline(added, modified, removed, moved int) string {
	total := added + modified + removed + moved
	if total == 0 {
		return "No changes proposed"
	}
	return Count(total, "change") + " proposed"
}

// SummaryParts returns the tinted counters shown under the headline.
// Order matches the semantic hierarchy: additions, changes, removals,
// moves.
func SummaryParts(added, modified, removed, moved int) []SummaryPart {
	var parts []SummaryPart
	if added > 0 {
		parts = append(parts, SummaryPart{Kind: "added", Label: fmt.Sprintf("+ %s", Count(added, "addition"))})
	}
	if modified > 0 {
		parts = append(parts, SummaryPart{Kind: "modified", Label: fmt.Sprintf("~ %s", Count(modified, "modification"))})
	}
	if removed > 0 {
		parts = append(parts, SummaryPart{Kind: "removed", Label: fmt.Sprintf("− %s", Count(removed, "removal"))})
	}
	if moved > 0 {
		parts = append(parts, SummaryPart{Kind: "moved", Label: fmt.Sprintf("↕ %s", Count(moved, "move"))})
	}
	return parts
}

// BulkAcceptLabel is the wording for the accept-everything action.
func BulkAcceptLabel(total int) string {
	if total == 0 {
		return "Accept all changes"
	}
	return "Accept all " + Count(total, "change")
}

// ApplyLabel reflects what the user has actually selected. The frontend
// mirrors this rule for live updates; the Go implementation is the source
// of truth and is covered by tests.
func ApplyLabel(selected int) string {
	switch selected {
	case 0:
		return "Apply selected changes"
	case 1:
		return "Apply 1 change"
	default:
		return fmt.Sprintf("Apply %d changes", selected)
	}
}

// StateLabel describes the current decision for a change.
func StateLabel(kind, state string) string {
	switch state {
	case "accepted":
		switch kind {
		case "added":
			return "Will be added"
		case "removed":
			return "Will be removed"
		case "moved":
			return "Will be moved"
		default:
			return "Will be changed"
		}
	case "rejected":
		switch kind {
		case "added":
			return "Not added"
		case "removed":
			return "Kept as is"
		case "moved":
			return "Kept in place"
		default:
			return "Kept as is"
		}
	default:
		return "Pending decision"
	}
}

// fieldLabels translates raw IR paths into language people understand.
var fieldLabels = map[string]string{
	"name":                "Name",
	"module":              "Implementation",
	"when":                "Only when",
	"register":            "Saves the result as",
	"delegate_to":         "Runs on a different machine",
	"changed_when":        "Counts as changed when",
	"failed_when":         "Counts as failed when",
	"become":              "Run with administrator privileges",
	"run_once":            "Runs only once",
	"until":               "Repeat until",
	"retries":             "Retries",
	"delay":               "Delay between retries (seconds)",
	"notify":              "Restarts",
	"tags":                "Tags",
	"loop":                "Repeats for each item",
	"environment":         "Environment variables",
	"include_tasks":       "Includes tasks from",
	"import_tasks":        "Imports tasks from",
	"play.name":           "Automation name",
	"play.hosts":          "Machines",
	"play.become":         "Run with administrator privileges",
	"play.vars":           "Variables",
	"args.name":           "Name",
	"args.path":           "Path",
	"args.dest":           "Destination",
	"args.src":            "Source",
	"args.content":        "Content",
	"args.state":          "State",
	"args.mode":           "Permissions",
	"args.owner":          "Owner",
	"args.group":          "Group",
	"args.enabled":        "Start automatically after reboot",
	"args.enable":         "Start automatically after reboot",
	"args.shell":          "Shell",
	"args.groups":         "Groups",
	"args.append":         "Add to existing groups",
	"args.key":            "Public key",
	"args.user":           "User",
	"args.ports":          "Published ports",
	"args.restart_policy": "Restart policy",
	"args.cmd":            "Command",
	"args.force":          "Replace existing",
	"args.url":            "URL",
	"args.that":           "Condition",
	"args.msg":            "Message",
	"args.image":          "Image",
	"args.package":        "Package",
	"args.update_cache":   "Refresh the package list first",
	"args.regexp":         "Pattern",
	"args.line":           "Line",
	"args.section":        "Section",
	"args.block":          "Block",
	"args.create_home":    "Create home directory",
	"args.system":         "System account",
	"args.uid":            "User ID",
	"args.gid":            "Group ID",
	"args.packages":       "Packages",
	"args.timeout":        "Timeout",
}

// FieldLabel returns the friendly name for a raw change path.
func FieldLabel(path string) string {
	if l, ok := fieldLabels[path]; ok {
		return l
	}
	// args.<key> for unknown keys: turn snake_case into words.
	if key, ok := strings.CutPrefix(path, "args."); ok {
		if _, nested, found := strings.Cut(key, "."); found {
			// Nested argument: label the leaf, keeping the parent visible.
			return FieldLabel("args."+nested) + " (" + humanizeKey(strings.SplitN(key, ".", 2)[0]) + ")"
		}
		return humanizeKey(key)
	}
	return humanizeKey(strings.TrimPrefix(path, "play."))
}

func humanizeKey(key string) string {
	words := strings.Split(key, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// fieldView converts one raw field change into a renderable view.
func fieldView(fc aidiff.FieldChange) FieldView {
	v := FieldView{
		Path:  fc.Path,
		Label: FieldLabel(fc.Path),
	}
	v.ValueKind = valueKind(fc.From, fc.To)
	v.From = displayValue(fc.From)
	v.To = displayValue(fc.To)
	if lv := longValue(fc.From); lv != nil {
		v.FromLong = lv
		v.From = ""
	}
	if lv := longValue(fc.To); lv != nil {
		v.ToLong = lv
		v.To = ""
	}
	if list, ok := stringList(fc.From); ok {
		v.FromList = list
		v.From = ""
		v.ValueKind = "list"
	}
	if list, ok := stringList(fc.To); ok {
		v.ToList = list
		v.To = ""
		v.ValueKind = "list"
	}
	return v
}

func valueKind(from, to any) string {
	if _, ok := from.(bool); ok {
		return "bool"
	}
	if _, ok := to.(bool); ok {
		return "bool"
	}
	return "text"
}

// displayValue renders a value for humans: booleans become Yes/No and
// absent values are named rather than left blank.
func displayValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "(not set)"
	case bool:
		if x {
			return "Yes"
		}
		return "No"
	case string:
		if strings.TrimSpace(x) == "" {
			return "(empty)"
		}
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprint(x)
	}
}

func stringList(v any) ([]string, bool) {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, fmt.Sprint(item))
		}
		return out, true
	case []string:
		return x, true
	}
	return nil, false
}

// longValue summarizes oversized values so cards stay scannable.
func longValue(v any) *action.LongValue {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	lines := strings.Count(s, "\n") + 1
	if len(s) <= 160 && lines <= 4 {
		return nil
	}
	preview := s
	if lines > 3 {
		parts := strings.Split(s, "\n")
		preview = strings.Join(append(parts[:3], "…"), "\n")
	}
	if len(preview) > 200 {
		preview = strings.TrimSpace(preview[:200]) + "…"
	}
	return &action.LongValue{Lines: lines, Chars: len(s), Preview: preview}
}

// MergeFailureMessage explains why a selected subset cannot be applied,
// in the user's language.
func MergeFailureMessage(errText string) string {
	lower := strings.ToLower(errText)
	switch {
	case strings.Contains(lower, "unknown handler"):
		name := quoteAfter(errText, "unknown handler")
		if name == "" {
			return "This selection cannot be applied: a step restarts a service whose automation you kept out of the proposal."
		}
		return fmt.Sprintf(
			"This selection cannot be applied: a step restarts “%s”, but the automation that provides it was not selected. "+
				"Accept that change too, or reject the step that needs it.", name)
	case strings.Contains(lower, "module is required"):
		return "This selection cannot be applied: one of the steps would be left without anything to do. Keep the step that fills it in, or reject the empty one."
	case strings.Contains(lower, "hosts is required"):
		return "This selection cannot be applied: the automation would be left without any machines to run on."
	case strings.Contains(lower, "not a mapping"), strings.Contains(lower, "invalid"):
		return "This selection cannot be applied because the result would not be valid automation. Try accepting fewer changes."
	default:
		return "This selection cannot be applied: " + errText
	}
}

// quoteAfter returns the first quoted fragment that appears after the
// given phrase, which is where the offending identifier lives (the raw
// message also quotes playbook and task names earlier in the text).
func quoteAfter(s, phrase string) string {
	idx := strings.Index(strings.ToLower(s), strings.ToLower(phrase))
	if idx < 0 {
		idx = 0
	}
	rest := s[idx:]
	start := strings.IndexAny(rest, "\"“")
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.IndexAny(rest, "\"”")
	if end < 0 {
		return ""
	}
	return rest[:end]
}
