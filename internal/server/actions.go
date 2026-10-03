package server

import (
	"encoding/json"
	"net/http"

	"github.com/dotfrankruan/visualible/internal/action"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/render"
)

// --- Curated Actions API ---
//
// Actions are a presentation layer over the IR: generation produces
// ordinary tasks, recognition maps tasks back when faithful. The full
// module browser remains the long tail for everything else.

type actionListResponse struct {
	Actions []action.Definition `json:"actions"`
}

func (s *Server) handleActionList(w http.ResponseWriter, r *http.Request) {
	defs := action.Definitions()
	s.applyActionAvailability(r, defs)
	writeJSON(w, http.StatusOK, actionListResponse{Actions: defs})
}

// applyActionAvailability hides/disables Actions whose required
// collection is not installed (e.g. Docker without community.docker).
func (s *Server) applyActionAvailability(r *http.Request, defs []action.Definition) {
	collections := map[string]bool{}
	known := false
	if s.discovery != nil {
		if mods, err := s.discovery.Modules(r.Context(), false); err == nil {
			known = true
			for _, m := range mods {
				if c := m.Collection; c != "" {
					collections[c] = true
				}
			}
		}
	}
	for i := range defs {
		req := defs[i].RequiresCollection
		if req == "" {
			defs[i].Available = true
			continue
		}
		if known && collections[req] {
			defs[i].Available = true
			continue
		}
		defs[i].Available = false
		if known {
			defs[i].Reason = "requires the " + req + " collection"
		} else {
			defs[i].Reason = "requires Ansible and the " + req + " collection"
		}
	}
}

type actionGenerateRequest struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
}

func (s *Server) handleActionGenerate(w http.ResponseWriter, r *http.Request) {
	var req actionGenerateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	gen, err := action.Generate(req.Action, req.Params)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "action_failed", err.Error())
		return
	}
	// Generated tasks must validate and render before they reach the
	// editor — the curated path gets no shortcuts.
	probe := &ir.Playbook{ID: "probe", Name: "probe", Plays: []*ir.Play{{
		ID: "probe", Name: "probe", Hosts: "all", Tasks: gen.Tasks, Handlers: gen.Handlers,
	}}}
	if err := probe.Validate(); err != nil {
		writeError(w, http.StatusInternalServerError, "action_invalid",
			"generated automation failed validation: "+err.Error())
		return
	}
	if _, err := render.Playbook(probe); err != nil {
		writeError(w, http.StatusInternalServerError, "action_invalid",
			"generated automation could not be rendered: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gen)
}

type actionRecognizeRequest struct {
	Tasks []*ir.Task `json:"tasks"`
}

type actionRecognizeResponse struct {
	Recognitions []action.Recognition `json:"recognitions"`
}

func (s *Server) handleActionRecognize(w http.ResponseWriter, r *http.Request) {
	var req actionRecognizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	recs := action.RecognizeAll(req.Tasks)
	if recs == nil {
		recs = []action.Recognition{}
	}
	writeJSON(w, http.StatusOK, actionRecognizeResponse{Recognitions: recs})
}
