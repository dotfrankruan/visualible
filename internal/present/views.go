// Package present turns semantic diff changes into review-ready
// descriptions for people who do not know Ansible.
//
// It is display-only: the IR and the diff engine remain authoritative.
// Every value shown here comes from deterministic rules over the real
// tasks — nothing is invented, and anything that cannot be translated
// confidently is labelled an Advanced Ansible task.
package present

import (
	"fmt"
	"strings"

	"github.com/dotfrankruan/visualible/internal/action"
	"github.com/dotfrankruan/visualible/internal/aidiff"
	"github.com/dotfrankruan/visualible/internal/ir"
)

// FieldView is one changed field, ready to render side by side.
type FieldView struct {
	Path     string   `json:"path"`  // raw path, for experts
	Label    string   `json:"label"` // friendly label
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	FromList []string `json:"fromList,omitempty"`
	ToList   []string `json:"toList,omitempty"`
	// ValueKind is text | bool | list | long | number.
	ValueKind string `json:"valueKind"`
	// Long marks a summarized value (large content, preview available).
	FromLong *action.LongValue `json:"fromLong,omitempty"`
	ToLong   *action.LongValue `json:"toLong,omitempty"`
}

// ChangeView describes one proposed change for review.
type ChangeView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`     // added | modified | removed | moved
	KindRank string `json:"kindRank"` // ADDED | MODIFIED | REMOVED | MOVED
	Icon     string `json:"icon"`     // + ~ − ↕
	Section  string `json:"section"`  // tasks | handlers | pre_tasks | post_tasks | play
	Handler  bool   `json:"handler,omitempty"`
	// Destructive marks changes that delete existing automation.
	Destructive bool `json:"destructive,omitempty"`

	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`

	// Details describe what the change does, in machine-level language:
	// for additions the resulting task, for removals what currently exists.
	Details []action.Detail `json:"details,omitempty"`
	Risks   []string        `json:"risks,omitempty"`

	// FieldChanges lists only what differs (modified items).
	FieldChanges []FieldView `json:"fieldChanges,omitempty"`
	// ResultDetails describe the resulting automation, so unchanged parts
	// are available on demand without competing with the change itself.
	ResultDetails []action.Detail `json:"resultDetails,omitempty"`

	// Module identities and raw arguments for the technical-details
	// expander. Nothing here is rewritten — it is what Ansible will run.
	Module        string          `json:"module,omitempty"`
	CurrentModule string          `json:"currentModule,omitempty"`
	Arguments     []action.Detail `json:"arguments,omitempty"`
	CurrentArgs   []action.Detail `json:"currentArguments,omitempty"`
	// CurrentDetails / CurrentNotifyExcerpt give the "current behaviour" view
	// for removals.
	CurrentDetails []action.Detail `json:"currentDetails,omitempty"`

	AcceptLabel string `json:"acceptLabel"`
	RejectLabel string `json:"rejectLabel"`

	// DependsOn names other changes this one relies on.
	DependsOn []string `json:"dependsOn,omitempty"`
}

// ProposalView is the complete review model for a proposal.
type ProposalView struct {
	Headline     string        `json:"headline"`
	SummaryParts []SummaryPart `json:"summaryParts"`
	HasRemovals  bool          `json:"hasRemovals"`
	Removals     int           `json:"removals"`

	Additions     int `json:"additions"`
	Modifications int `json:"modifications"`
	Deletions     int `json:"deletions"`
	Moves         int `json:"moves"`

	BulkAcceptLabel string `json:"bulkAcceptLabel"`
	BulkAcceptNote  string `json:"bulkAcceptNote,omitempty"`
	BulkRejectLabel string `json:"bulkRejectLabel"`

	Changes []ChangeView `json:"changes"`
}

// SummaryPart is one tinted counter in the summary header.
type SummaryPart struct {
	Kind  string `json:"kind"`  // added | modified | removed | moved
	Label string `json:"label"` // "+ 3 added"
}

// BuildView assembles the review model for a proposal diff.
func BuildView(base, proposed *ir.Playbook, d *aidiff.Diff) *ProposalView {
	v := &ProposalView{}
	if d == nil {
		return v
	}
	v.Additions = d.Summary.Added
	v.Modifications = d.Summary.Modified
	v.Deletions = d.Summary.Removed
	v.Moves = d.Summary.Moved

	// Play-level settings first: they frame everything below.
	if len(d.PlayChanges) > 0 {
		v.Changes = append(v.Changes, playChangeView(d.PlayChanges))
	}

	// Changes in execution order (the diff engine already orders by
	// position within each section, and sections run in order).
	index := map[string]*ChangeView{}
	for i := range d.Changes {
		c := d.Changes[i]
		if c.Kind == aidiff.Unchanged {
			continue
		}
		view := changeView(c)
		v.Changes = append(v.Changes, view)
		index[c.ID] = &v.Changes[len(v.Changes)-1]
	}
	resolveDependencies(v, index)

	// Summary wording.
	v.Headline = Headline(v.Additions, v.Modifications, v.Deletions, v.Moves)
	v.SummaryParts = SummaryParts(v.Additions, v.Modifications, v.Deletions, v.Moves)
	v.HasRemovals = v.Deletions > 0
	v.Removals = v.Deletions
	total := v.Additions + v.Modifications + v.Deletions + v.Moves
	v.BulkAcceptLabel = BulkAcceptLabel(total)
	if v.Removals > 0 {
		v.BulkAcceptNote = fmt.Sprintf("Includes %s", Count(v.Removals, "removal"))
	}
	v.BulkRejectLabel = "Reject all"
	return v
}

func playChangeView(changes []aidiff.FieldChange) ChangeView {
	view := ChangeView{
		ID:          aidiff.PlayChangeID,
		Kind:        "modified",
		KindRank:    "MODIFIED",
		Icon:        "~",
		Section:     "play",
		Title:       "Automation settings",
		AcceptLabel: "Accept change",
		RejectLabel: "Keep current",
	}
	for _, fc := range changes {
		view.FieldChanges = append(view.FieldChanges, fieldView(fc))
	}
	return view
}

func changeView(c aidiff.Change) ChangeView {
	view := ChangeView{
		ID:          c.ID,
		Kind:        string(c.Kind),
		Section:     c.Section,
		Handler:     c.Section == "handlers",
		KindRank:    strings.ToUpper(string(c.Kind)),
		Module:      taskModule(c.Proposed),
		Title:       c.Name,
		AcceptLabel: "Accept",
		RejectLabel: "Reject",
	}
	if view.Module == "" {
		view.Module = taskModule(c.Base)
	}

	view.Arguments = action.RawArguments(c.Proposed)
	view.CurrentArgs = action.RawArguments(c.Base)

	switch c.Kind {
	case aidiff.Added:
		view.Icon = "+"
		view.AcceptLabel = "Add"
		view.RejectLabel = "Reject"
		p := action.Present(c.Proposed, view.Handler)
		view.Title = p.Title
		view.Subtitle = p.Subtitle
		view.Details = p.Details
		view.Risks = p.Risks
	case aidiff.Modified:
		view.Icon = "~"
		view.AcceptLabel = "Accept change"
		view.RejectLabel = "Keep current"
		p := action.Present(c.Proposed, view.Handler)
		view.Title = p.Title
		view.Subtitle = p.Subtitle
		view.ResultDetails = p.Details
		view.Risks = p.Risks
		for _, fc := range c.Fields {
			view.FieldChanges = append(view.FieldChanges, fieldView(fc))
		}
	case aidiff.Removed:
		view.Icon = "−"
		view.Destructive = true
		view.AcceptLabel = "Accept removal"
		view.RejectLabel = "Keep current"
		cur := action.Present(c.Base, view.Handler)
		view.Title = cur.Title
		view.Subtitle = cur.Subtitle
		view.CurrentDetails = cur.Details
		view.CurrentModule = c.Base.Module
		view.Risks = cur.Risks
	case aidiff.Moved:
		view.Icon = "↕"
		view.AcceptLabel = "Accept move"
		view.RejectLabel = "Keep position"
		p := action.Present(c.Proposed, view.Handler)
		view.Title = p.Title
		view.Details = []action.Detail{
			{Label: "Position", Value: fmt.Sprintf("moves from %d to %d", c.FromPos+1, c.ToPos+1)},
		}
	}
	return view
}

func taskModule(t *ir.Task) string {
	if t == nil {
		return ""
	}
	return t.Module
}

// resolveDependencies links changes that rely on each other, so review can
// explain why rejecting one invalidates another.
func resolveDependencies(v *ProposalView, index map[string]*ChangeView) {
	// Paths written by a change, so a later link/enable can point at it.
	writtenBy := map[string]string{}
	for _, c := range v.Changes {
		if c.Kind == "removed" {
			continue
		}
		for _, d := range append(append([]action.Detail{}, c.Details...), c.ResultDetails...) {
			if d.Label == "Configuration file" || d.Label == "File" || d.Label == "Folder" {
				if d.Value != "" {
					writtenBy[d.Value] = c.ID
				}
			}
		}
	}
	for i := range v.Changes {
		c := &v.Changes[i]
		// Enabling/linking a file depends on whatever creates that file.
		for _, d := range c.Details {
			if d.Label == "Points to" && d.Value != "" {
				if id, ok := writtenBy[d.Value]; ok && id != c.ID {
					c.DependsOn = appendUnique(c.DependsOn, titleOf(index, id))
				}
			}
		}
	}
}

func titleOf(index map[string]*ChangeView, id string) string {
	if c, ok := index[id]; ok {
		return c.Title
	}
	return id
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	if s == "" {
		return list
	}
	return append(list, s)
}
