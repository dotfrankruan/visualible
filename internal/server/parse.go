package server

import (
	"encoding/json"
	"net/http"

	"github.com/visualible/visualible/internal/parse"
)

// --- Parse (YAML import) API ---

type parseRequest struct {
	YAML string `json:"yaml"`
	Name string `json:"name,omitempty"`
}

type parseResponse struct {
	Playbook    any                `json:"playbook,omitempty"`
	Diagnostics []parse.Diagnostic `json:"diagnostics"`
}

func (s *Server) handleParse(w http.ResponseWriter, r *http.Request) {
	var req parseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	name := req.Name
	if name == "" {
		name = "imported"
	}
	res, err := parse.Playbook(name, []byte(req.YAML))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "parse_failed", err.Error())
		return
	}
	// Imported content is untrusted: surface validation problems as
	// diagnostics too, so nothing is silently accepted.
	if verr := res.Playbook.Validate(); verr != nil {
		res.Diagnostics = append(res.Diagnostics, parse.Diagnostic{
			Severity: parse.Warning,
			Path:     "playbook",
			Message:  "imported playbook does not fully validate: " + verr.Error(),
		})
	}
	writeJSON(w, http.StatusOK, parseResponse{
		Playbook:    res.Playbook,
		Diagnostics: res.Diagnostics,
	})
}
