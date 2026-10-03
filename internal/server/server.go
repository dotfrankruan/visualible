// Package server exposes Visualible's REST API and serves the embedded
// frontend. It uses only the standard library: Go 1.22+ pattern routing
// on net/http. API responses use typed structs; errors are structured.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dotfrankruan/visualible/internal/ansible"
	"github.com/dotfrankruan/visualible/internal/deploy"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/logging"
	"github.com/dotfrankruan/visualible/internal/render"
	"github.com/dotfrankruan/visualible/internal/store"
	webfs "github.com/dotfrankruan/visualible/internal/web"
)

// Server wires the API to domain services.
type Server struct {
	discovery *ansible.Discovery
	store     *store.Store
	manager   *deploy.Manager
	mux       *http.ServeMux
}

// New builds a Server with all routes registered. store and manager may
// be nil in tests (their endpoints then return 503).
func New(discovery *ansible.Discovery, st *store.Store, manager *deploy.Manager) (*Server, error) {
	s := &Server{discovery: discovery, store: st, manager: manager, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	withRequestLogging(s.mux).ServeHTTP(w, r)
}

// --- Structured API errors ---

type apiError struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Problems []string `json:"problems,omitempty"`
}

type errorResponse struct {
	Error apiError `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string, problems ...string) {
	writeJSON(w, status, errorResponse{Error: apiError{Code: code, Message: message, Problems: problems}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logging.Error("could not encode API response", "err", err)
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/modules", s.handleModuleList)
	s.mux.HandleFunc("GET /api/modules/{fqcn}", s.handleModuleDoc)
	s.mux.HandleFunc("POST /api/render", s.handleRender)
	s.mux.HandleFunc("POST /api/parse", s.handleParse)
	s.mux.HandleFunc("POST /api/render/inventory", s.handleRenderInventory)

	s.mux.HandleFunc("GET /api/projects", s.handleProjectList)
	s.mux.HandleFunc("POST /api/projects", s.handleProjectCreate)
	s.mux.HandleFunc("GET /api/projects/{id}", s.handleProjectGet)
	s.mux.HandleFunc("PUT /api/projects/{id}", s.handleProjectPut)
	s.mux.HandleFunc("DELETE /api/projects/{id}", s.handleProjectDelete)

	s.mux.HandleFunc("GET /api/credentials", s.handleCredentialList)
	s.mux.HandleFunc("POST /api/credentials", s.handleCredentialCreate)
	s.mux.HandleFunc("DELETE /api/credentials/{id}", s.handleCredentialDelete)

	s.mux.HandleFunc("GET /api/deploy/backend", s.handleBackendInfo)
	s.mux.HandleFunc("POST /api/deployments", s.handleDeploymentCreate)
	s.mux.HandleFunc("GET /api/deployments", s.handleDeploymentList)
	s.mux.HandleFunc("GET /api/deployments/{id}", s.handleDeploymentGet)
	s.mux.HandleFunc("POST /api/deployments/{id}/cancel", s.handleDeploymentCancel)
	s.mux.HandleFunc("GET /api/deployments/{id}/events", s.handleDeploymentEvents)

	s.mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	s.mux.HandleFunc("PUT /api/settings", s.handleSettingsPut)
	s.mux.HandleFunc("POST /api/ai/generate", s.handleAIGenerate)
	s.mux.HandleFunc("POST /api/ai/proposals", s.handleAIProposalCreate)
	s.mux.HandleFunc("POST /api/ai/merge", s.handleAIMerge)

	s.mux.HandleFunc("GET /api/actions", s.handleActionList)
	s.mux.HandleFunc("POST /api/actions/generate", s.handleActionGenerate)
	s.mux.HandleFunc("POST /api/actions/recognize", s.handleActionRecognize)

	s.mux.HandleFunc("POST /api/targets/test", s.handleTargetTest)
	s.mux.HandleFunc("POST /api/deployments/preflight", s.handlePreflight)

	// Embedded frontend; anything not under /api falls through to static.
	if static, err := webfs.Static(); err == nil {
		s.mux.Handle("GET /", http.FileServer(http.FS(static)))
	} else {
		logging.Error("embedded frontend unavailable", "err", err)
	}
}

// --- Health ---

// Version is the application version, set by main at startup.
var Version = "dev"

type healthResponse struct {
	Version string         `json:"version"`
	Ansible ansible.Status `json:"ansible"`
	Modules int            `json:"modules"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{Version: Version, Ansible: s.discovery.Status()}
	if list, ok := s.discovery.CachedModules(); ok {
		resp.Modules = len(list)
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Modules ---

type moduleListResponse struct {
	Modules []ansible.ModuleSummary `json:"modules"`
	Cached  bool                    `json:"cached"`
}

func (s *Server) handleModuleList(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	modules, err := s.discovery.Modules(r.Context(), refresh)
	if err != nil {
		if errors.Is(err, ansible.ErrNotFound) {
			writeError(w, http.StatusServiceUnavailable, "ansible_unavailable",
				"Ansible was not detected on this machine. Install Ansible and restart Visualible to discover modules.")
			return
		}
		writeError(w, http.StatusBadGateway, "ansible_doc_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, moduleListResponse{Modules: modules})
}

func (s *Server) handleModuleDoc(w http.ResponseWriter, r *http.Request) {
	fqcn := r.PathValue("fqcn")
	refresh := r.URL.Query().Get("refresh") == "1"
	schema, err := s.discovery.Module(r.Context(), fqcn, refresh)
	if err != nil {
		switch {
		case errors.Is(err, ansible.ErrNotFound):
			writeError(w, http.StatusServiceUnavailable, "ansible_unavailable",
				"Ansible was not detected on this machine.")
		case strings.Contains(err.Error(), "invalid module name"):
			writeError(w, http.StatusBadRequest, "invalid_fqcn", err.Error())
		default:
			writeError(w, http.StatusBadGateway, "ansible_doc_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

// --- Render ---

type renderRequest struct {
	Playbook *ir.Playbook `json:"playbook"`
}

type renderResponse struct {
	YAML string `json:"yaml"`
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	var req renderRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
		return
	}
	if req.Playbook == nil {
		writeError(w, http.StatusBadRequest, "missing_playbook", "playbook is required")
		return
	}
	out, err := render.Playbook(req.Playbook)
	if err != nil {
		var ve *ir.ValidationError
		var ue *render.UnsupportedError
		switch {
		case errors.As(err, &ve):
			writeError(w, http.StatusUnprocessableEntity, "invalid_playbook", "playbook failed validation", ve.Problems...)
		case errors.As(err, &ue):
			writeError(w, http.StatusUnprocessableEntity, "unsupported_features", ue.Error(), ue.Features...)
		default:
			writeError(w, http.StatusInternalServerError, "render_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, renderResponse{YAML: string(out)})
}
