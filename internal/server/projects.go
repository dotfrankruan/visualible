package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/logging"
	"github.com/dotfrankruan/visualible/internal/store"
)

// --- Projects API ---

type projectListResponse struct {
	Projects []store.ProjectMeta `json:"projects"`
}

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (s *Server) requireStore(w http.ResponseWriter) bool {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "project storage is not configured")
		return false
	}
	return true
}

func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	metas, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	if metas == nil {
		metas = []store.ProjectMeta{}
	}
	writeJSON(w, http.StatusOK, projectListResponse{Projects: metas})
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "name is required")
		return
	}
	p := &ir.Project{
		ID:          newID("proj"),
		Name:        req.Name,
		Description: req.Description,
		Playbooks: []*ir.Playbook{{
			ID:   newID("pb"),
			Name: "playbook",
			Plays: []*ir.Play{{
				ID:    newID("play"),
				Name:  "New play",
				Hosts: "all",
				Tasks: []*ir.Task{},
			}},
		}},
		Inventories: []*ir.Inventory{},
	}
	if err := s.store.SaveProject(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	logging.Info("project created", "project", p.ID, "name", p.Name)
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	p, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleProjectPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	id := r.PathValue("id")
	var p ir.Project
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if p.ID != id {
		writeError(w, http.StatusBadRequest, "id_mismatch", "path id and body id differ")
		return
	}
	if err := p.Validate(); err != nil {
		if ve, ok := err.(*ir.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_project", "project failed validation", ve.Problems...)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "invalid_project", err.Error())
		return
	}
	if err := s.store.SaveProject(r.Context(), &p); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	logging.Debug("project updated", "project", p.ID, "name", p.Name)
	writeJSON(w, http.StatusOK, &p)
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	err := s.store.DeleteProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newID generates a random, URL-safe internal ID. IDs are for graph and
// editing stability only; they never appear in exported Ansible YAML.
func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%x", prefix, b)
}
