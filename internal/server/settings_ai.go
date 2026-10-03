package server

import (
	"encoding/json"
	"net/http"

	"github.com/dotfrankruan/visualible/internal/ai"
	"github.com/dotfrankruan/visualible/internal/parse"
	"github.com/dotfrankruan/visualible/internal/store"
)

// --- Settings API ---
//
// store.Settings contains only credential *references* (IDs) for secret
// material, so responses never expose secrets by construction.

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	st, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	var st store.Settings
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.store.SaveSettings(r.Context(), &st); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	// Apply Ansible path overrides immediately.
	if s.discovery != nil {
		s.discovery.ApplyOverrides(r.Context(), st.AnsiblePath, st.AnsibleDocPath, st.AnsiblePlaybookPath)
	}
	writeJSON(w, http.StatusOK, &st)
}

// --- AI API ---

type aiGenerateRequest struct {
	Intent      string `json:"intent"`
	CurrentYAML string `json:"currentYaml,omitempty"`
}

type aiGenerateResponse struct {
	Playbook    any                `json:"playbook,omitempty"`
	Diagnostics []parse.Diagnostic `json:"diagnostics"`
}

func (s *Server) handleAIGenerate(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	var req aiGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	provider, _, err := s.aiProvider(r)
	if err != nil {
		writeError(w, http.StatusPreconditionFailed, "ai_not_configured",
			"AI is not configured. Set the endpoint and model in Settings.")
		return
	}
	res, _, err := ai.GeneratePlaybook(r.Context(), provider, req.Intent, req.CurrentYAML)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ai_generation_failed", err.Error())
		return
	}
	if verr := res.Playbook.Validate(); verr != nil {
		res.Diagnostics = append(res.Diagnostics, parse.Diagnostic{
			Severity: parse.Warning,
			Path:     "playbook",
			Message:  "generated playbook does not fully validate: " + verr.Error(),
		})
	}
	writeJSON(w, http.StatusOK, aiGenerateResponse{
		Playbook:    res.Playbook,
		Diagnostics: res.Diagnostics,
	})
}
