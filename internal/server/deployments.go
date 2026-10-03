package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dotfrankruan/visualible/internal/deploy"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/store"
)

// --- Deployments API ---

type backendInfoResponse struct {
	Metadata     deploy.BackendMetadata `json:"metadata"`
	Capabilities deploy.Capabilities    `json:"capabilities"`
}

func (s *Server) handleBackendInfo(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "deployment_unavailable", "no deployment backend is configured")
		return
	}
	writeJSON(w, http.StatusOK, backendInfoResponse{
		Metadata:     s.manager.Backend().Metadata(),
		Capabilities: s.manager.Backend().Capabilities(),
	})
}

type createDeploymentRequest struct {
	ProjectID   string   `json:"projectId"`
	PlaybookID  string   `json:"playbookId"`
	InventoryID string   `json:"inventoryId"`
	Tags        []string `json:"tags,omitempty"`
	Limit       string   `json:"limit,omitempty"`
	Check       bool     `json:"check,omitempty"`
	Diff        bool     `json:"diff,omitempty"`
	Verbosity   int      `json:"verbosity,omitempty"`
}

func (s *Server) handleDeploymentCreate(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil || s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "deployment_unavailable", "deployment is not configured")
		return
	}
	var req createDeploymentRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	plan := &ir.DeploymentPlan{
		ID:          newID("dep"),
		ProjectID:   req.ProjectID,
		PlaybookID:  req.PlaybookID,
		InventoryID: req.InventoryID,
		Tags:        req.Tags,
		Limit:       req.Limit,
		Check:       req.Check,
		Diff:        req.Diff,
		Verbosity:   req.Verbosity,
	}
	if err := plan.Validate(); err != nil {
		if ve, ok := err.(*ir.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_plan", "plan failed validation", ve.Problems...)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "invalid_plan", err.Error())
		return
	}
	project, err := s.store.GetProject(r.Context(), plan.ProjectID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	d, err := s.manager.Start(r.Context(), plan, project)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "deployment_rejected", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleDeploymentList(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "storage is not configured")
		return
	}
	list, err := s.store.ListDeployments(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	if list == nil {
		list = []ir.Deployment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": list})
}

func (s *Server) handleDeploymentGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.manager != nil {
		if d, ok := s.manager.Get(id); ok {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "storage is not configured")
		return
	}
	d, err := s.store.GetDeployment(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeploymentCancel(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "deployment_unavailable", "deployment is not configured")
		return
	}
	if err := s.manager.Cancel(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusConflict, "cancel_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleDeploymentEvents streams deployment events as Server-Sent Events:
// first a replay of persisted events, then live events until the
// deployment finishes or the client disconnects.
func (s *Server) handleDeploymentEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "response writer cannot stream")
		return
	}

	// Verify the deployment exists.
	if s.manager != nil {
		if _, ok := s.manager.Get(id); !ok && s.store != nil {
			if _, err := s.store.GetDeployment(r.Context(), id); errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func(ev ir.DeploymentEvent) error {
		data, err := json.Marshal(ev)
		if err != nil {
			return nil
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	// 1. Replay persisted events.
	if s.store != nil {
		stored, err := s.store.DeploymentEvents(r.Context(), id)
		if err == nil {
			for _, ev := range stored {
				if send(ev) != nil {
					return
				}
			}
		}
	}

	// 2. Live tail if still running.
	if s.manager != nil {
		ch, unsub, ok := s.manager.Subscribe(id, 256)
		if ok {
			defer unsub()
			heartbeat := time.NewTicker(15 * time.Second)
			defer heartbeat.Stop()
			for {
				select {
				case ev, open := <-ch:
					if !open {
						// Deployment finished: final status event.
						if d, ok := s.manager.Get(id); ok {
							fin := ir.DeploymentEvent{
								Type:         ir.EventDeploymentFinished,
								Timestamp:    time.Now().UTC(),
								DeploymentID: id,
								Status:       string(d.Status),
							}
							_ = send(fin)
						}
						return
					}
					if send(ev) != nil {
						return
					}
				case <-heartbeat.C:
					fmt.Fprintf(w, ": heartbeat\n\n")
					flusher.Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
	}
}
