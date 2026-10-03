package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/dotfrankruan/visualible/internal/ai"
	"github.com/dotfrankruan/visualible/internal/aidiff"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/logging"
)

// --- AI change-proposal API ---
//
// Flow: user intent (+ current IR) → model proposes complete desired IR →
// Visualible validates → Visualible diffs (never the model) → human
// reviews → selective accept/reject → merge → validate → apply. The
// model is never authoritative about what changed and never deploys.

// ProposalStatus tracks the lifecycle of a proposal.
type ProposalStatus string

const (
	ProposalGenerating        ProposalStatus = "generating"
	ProposalReady             ProposalStatus = "ready"
	ProposalStale             ProposalStatus = "stale"
	ProposalPartiallyAccepted ProposalStatus = "partially_accepted"
	ProposalApplied           ProposalStatus = "applied"
	ProposalRejected          ProposalStatus = "rejected"
	ProposalFailed            ProposalStatus = "failed"
)

// Proposal is the AI change-proposal document. Structured so future
// versions can persist it (proposals are ephemeral in v1 and never
// contain provider credentials).
type Proposal struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"projectId,omitempty"`
	BaseRevision int            `json:"baseRevision"`
	BaseHash     string         `json:"baseHash"`
	Request      string         `json:"request"`
	Model        string         `json:"model,omitempty"`
	ProposedIR   *ir.Playbook   `json:"proposedIr,omitempty"`
	Rationale    []string       `json:"rationale,omitempty"`
	Diff         *aidiff.Diff   `json:"diff,omitempty"`
	Diagnostics  []string       `json:"diagnostics,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	Status       ProposalStatus `json:"status"`
}

type aiProposalRequest struct {
	Intent       string       `json:"intent"`
	BaseRevision int          `json:"baseRevision"`
	Playbook     *ir.Playbook `json:"playbook"` // current working IR (may be unsaved)
}

func (s *Server) aiProvider(r *http.Request) (*ai.OpenAIProvider, string, error) {
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		return nil, "", err
	}
	cfg := ai.Config{
		Endpoint:    settings.AI.Endpoint,
		Model:       settings.AI.Model,
		Headers:     settings.AI.Headers,
		Temperature: settings.AI.Temperature,
	}
	if settings.AI.APIKeyCredID != "" {
		// Resolved server-side only; never logged, never returned.
		key, err := s.store.CredentialSecret(r.Context(), settings.AI.APIKeyCredID)
		if err != nil {
			return nil, "", err
		}
		cfg.APIKey = string(key)
	}
	if err := cfg.Validate(); err != nil {
		return nil, "", err
	}
	return ai.NewOpenAIProvider(cfg), settings.AI.Model, nil
}

func (s *Server) handleAIProposalCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	var req aiProposalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	provider, model, err := s.aiProvider(r)
	if err != nil {
		writeError(w, http.StatusPreconditionFailed, "ai_not_configured",
			"AI is not configured. Set the endpoint and model in Settings.")
		return
	}

	proposal := &Proposal{
		ID:           newID("prop"),
		BaseRevision: req.BaseRevision,
		Request:      req.Intent,
		Model:        model,
		CreatedAt:    time.Now().UTC(),
		Status:       ProposalGenerating,
	}

	base := req.Playbook
	if base == nil {
		base = &ir.Playbook{Plays: []*ir.Play{}}
	}
	if hash, err := aidiff.HashIR(base); err == nil {
		proposal.BaseHash = hash
	}

	logging.Info("ai proposal requested",
		"proposal", proposal.ID,
		"model", model,
		"baseRevision", req.BaseRevision,
		"baseHash", proposal.BaseHash,
		"tasksInBase", baseTaskCount(base),
		"intentChars", len(req.Intent))
	start := time.Now()
	proposed, err := ai.ProposeIR(r.Context(), provider, req.Intent, base)
	if err != nil {
		logging.Warn("ai proposal failed",
			"proposal", proposal.ID, "model", model,
			"duration", time.Since(start).Round(time.Millisecond), "err", err)
		// Invalid AI output must never mutate project state — and it
		// cannot: applying happens only through the merge endpoint,
		// which validates again.
		proposal.Status = ProposalFailed
		proposal.Diagnostics = []string{err.Error()}
		writeJSON(w, http.StatusOK, proposal)
		return
	}
	proposal.ProposedIR = proposed.Playbook
	proposal.Rationale = proposed.Rationale
	logging.Debug("ai response parsed",
		"proposal", proposal.ID,
		"duration", time.Since(start).Round(time.Millisecond),
		"tasks", baseTaskCount(proposed.Playbook),
		"rationaleLines", len(proposed.Rationale))

	// Validate before displaying as applicable.
	if verr := proposed.Playbook.Validate(); verr != nil {
		proposal.Status = ProposalFailed
		if ve, ok := verr.(*ir.ValidationError); ok {
			proposal.Diagnostics = ve.Problems
		} else {
			proposal.Diagnostics = []string{verr.Error()}
		}
		writeJSON(w, http.StatusOK, proposal)
		return
	}

	// Visualible computes the diff; the model is not consulted about it.
	proposal.Diff = aidiff.Playbooks(base, proposed.Playbook)
	proposal.Status = ProposalReady
	logging.Info("ai proposal ready",
		"proposal", proposal.ID,
		"added", proposal.Diff.Summary.Added,
		"modified", proposal.Diff.Summary.Modified,
		"removed", proposal.Diff.Summary.Removed,
		"moved", proposal.Diff.Summary.Moved,
		"unchanged", proposal.Diff.Summary.Unchanged)
	writeJSON(w, http.StatusOK, proposal)
}

// baseTaskCount counts tasks across a playbook's plays.
func baseTaskCount(pb *ir.Playbook) int {
	if pb == nil {
		return 0
	}
	n := 0
	for _, p := range pb.Plays {
		n += len(p.Tasks) + len(p.Handlers) + len(p.PreTasks) + len(p.PostTasks)
	}
	return n
}

type aiMergeRequest struct {
	Base     *ir.Playbook `json:"base"`
	Proposed *ir.Playbook `json:"proposed"`
	Accepted []string     `json:"accepted"` // change IDs
}

type aiMergeResponse struct {
	Playbook *ir.Playbook `json:"playbook"`
}

func (s *Server) handleAIMerge(w http.ResponseWriter, r *http.Request) {
	var req aiMergeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Base == nil || req.Proposed == nil {
		writeError(w, http.StatusBadRequest, "missing_ir", "base and proposed playbooks are required")
		return
	}
	d := aidiff.Playbooks(req.Base, req.Proposed)
	accepted := map[string]bool{}
	for _, id := range req.Accepted {
		accepted[id] = true
	}
	merged, err := aidiff.Merge(req.Base, req.Proposed, d, accepted)
	if err != nil {
		logging.Warn("ai merge rejected",
			"acceptedChanges", len(req.Accepted), "err", err)
		writeError(w, http.StatusUnprocessableEntity, "merge_invalid", err.Error())
		return
	}
	logging.Info("ai proposal applied",
		"acceptedChanges", len(req.Accepted),
		"totalChanges", len(d.Changes),
		"tasksAfter", baseTaskCount(merged))
	writeJSON(w, http.StatusOK, aiMergeResponse{Playbook: merged})
}
