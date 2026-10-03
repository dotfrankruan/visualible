package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/render"
)

// --- Inventory rendering API ---

type renderInventoryRequest struct {
	Inventory *ir.Inventory `json:"inventory"`
}

type renderInventoryResponse struct {
	YAML string `json:"yaml"`
}

func (s *Server) handleRenderInventory(w http.ResponseWriter, r *http.Request) {
	var req renderInventoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Inventory == nil {
		writeError(w, http.StatusBadRequest, "missing_inventory", "inventory is required")
		return
	}
	out, err := render.Inventory(req.Inventory)
	if err != nil {
		var ve *ir.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_inventory", "inventory failed validation", ve.Problems...)
			return
		}
		writeError(w, http.StatusInternalServerError, "render_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, renderInventoryResponse{YAML: string(out)})
}
