package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dotfrankruan/visualible/internal/deploy"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/store"
)

// --- Targets: connection testing + deployment preflight ---

func (s *Server) connectionTester(w http.ResponseWriter) (deploy.ConnectionTester, bool) {
	if s.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "deployment_unavailable", "no deployment backend is configured")
		return nil, false
	}
	tester, ok := s.manager.Backend().(deploy.ConnectionTester)
	if !ok {
		writeError(w, http.StatusNotImplemented, "connection_test_unsupported",
			"the configured deployment backend cannot test connections")
		return nil, false
	}
	return tester, true
}

type testTargetRequest struct {
	Host *ir.Host `json:"host"`
}

func (s *Server) handleTargetTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	tester, ok := s.connectionTester(w)
	if !ok {
		return
	}
	var req testTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Host == nil || req.Host.Name == "" {
		writeError(w, http.StatusBadRequest, "missing_host", "a machine with a name is required")
		return
	}
	res := tester.TestConnection(r.Context(), req.Host, s.store)
	writeJSON(w, http.StatusOK, res)
}

type preflightRequest struct {
	ProjectID   string `json:"projectId"`
	InventoryID string `json:"inventoryId"`
}

func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	tester, ok := s.connectionTester(w)
	if !ok {
		return
	}
	var req preflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	project, err := s.store.GetProject(r.Context(), req.ProjectID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	var inv *ir.Inventory
	for _, x := range project.Inventories {
		if x.ID == req.InventoryID {
			inv = x
		}
	}
	if inv == nil {
		writeError(w, http.StatusNotFound, "inventory_not_found", "inventory was not found in this project")
		return
	}
	res := tester.TestConnections(r.Context(), inv, s.store)
	writeJSON(w, http.StatusOK, res)
}
