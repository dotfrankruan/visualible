package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dotfrankruan/visualible/internal/store"
)

// --- Credentials API ---
//
// Secrets are write-only over the API: they can be stored and rotated,
// but no endpoint ever returns secret material. Metadata responses use
// store.CredentialMeta, which has no secret field by construction.

type credentialListResponse struct {
	Credentials []store.CredentialMeta `json:"credentials"`
}

type createCredentialRequest struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Secret string `json:"secret"`
}

func (s *Server) handleCredentialList(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	metas, err := s.store.ListCredentials(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, credentialListResponse{Credentials: metas})
}

func (s *Server) handleCredentialCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	var req createCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	meta, err := s.store.SaveCredential(r.Context(), newID("cred"), req.Name, req.Kind, []byte(req.Secret))
	// Best-effort: do not retain the secret in the request any longer.
	req.Secret = ""
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_credential", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) handleCredentialDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	err := s.store.DeleteCredential(r.Context(), r.PathValue("id"))
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
