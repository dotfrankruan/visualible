package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/dotfrankruan/visualible/internal/artifact"
	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/logging"
	"github.com/dotfrankruan/visualible/internal/render"
	"github.com/dotfrankruan/visualible/internal/store"
)

// --- Export API ---
//
// Export is a first-class product surface: automation authored in
// Visualible must be downloadable as standard Ansible YAML at any time.
// The exporter renders the stored document, so what lands on disk is
// exactly what the canonical renderer produces.

type exportFormat struct {
	ID          string
	Extension   string
	ContentType string
}

var exportFormats = map[string]exportFormat{
	"yaml": {ID: "yaml", Extension: "yml", ContentType: "application/yaml; charset=utf-8"},
	"yml":  {ID: "yaml", Extension: "yml", ContentType: "application/yaml; charset=utf-8"},
}

// handlePlaybookExport streams one playbook as a downloadable artifact.
// GET /api/projects/{id}/playbooks/{playbookId}/export/{format}
func (s *Server) handlePlaybookExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	format, ok := exportFormats[strings.ToLower(r.PathValue("format"))]
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported_format",
			"unsupported export format; supported: yaml")
		return
	}
	project, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	pb := findPlaybook(project, r.PathValue("playbookId"))
	if pb == nil {
		writeError(w, http.StatusNotFound, "playbook_not_found", "that playbook is not in this project")
		return
	}

	art, err := exportArtifact(format, pb)
	if err != nil {
		var ve *ir.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_playbook",
				"this playbook cannot be exported until it is valid", ve.Problems...)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "export_failed", err.Error())
		return
	}

	filename := uniqueInProject(project, art.Filename, pb.ID)
	logging.Info("playbook exported",
		"project", project.ID, "playbook", pb.ID, "format", format.ID,
		"filename", filename, "bytes", len(art.Data))

	w.Header().Set("Content-Type", art.ContentType)
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	w.Header().Set("X-Visualible-Filename", filename)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(art.Data); err != nil {
		logging.Warn("export write failed", "playbook", pb.ID, "err", err)
	}
}

// exportArtifact dispatches to the artifact exporter.
func exportArtifact(format exportFormat, pb *ir.Playbook) (*artifact.Artifact, error) {
	switch format.ID {
	case "yaml":
		return artifact.YAMLExporter{}.ExportPlaybook(pb)
	default:
		// The artifact abstraction is the extension point for future
		// formats (project bundles, Git, S3, role export).
		return nil, fmt.Errorf("no exporter is registered for %q", format.ID)
	}
}

// uniqueInProject keeps exported filenames distinct from the other
// playbooks in the same project, so a downloaded folder never silently
// loses a document to an overwrite.
func uniqueInProject(project *ir.Project, filename, exportID string) string {
	taken := map[string]bool{}
	for _, other := range project.Playbooks {
		if other == nil || other.ID == exportID {
			continue
		}
		taken[artifact.PlaybookFilename(other.Name, "yml")] = true
	}
	return artifact.UniqueFilename(filename, taken)
}

// contentDisposition builds an RFC 6266 header that keeps unicode names
// readable while staying valid for plain ASCII clients.
func contentDisposition(filename string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '-'
		}
		return r
	}, filename)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		ascii, url.PathEscape(filename))
}

// findPlaybook locates a playbook by ID within a project.
func findPlaybook(project *ir.Project, id string) *ir.Playbook {
	for _, pb := range project.Playbooks {
		if pb != nil && pb.ID == id {
			return pb
		}
	}
	return nil
}

// renderPlaybookYAML is used by the JSON export preview endpoint.
func renderPlaybookYAML(pb *ir.Playbook) (string, error) {
	out, err := render.Playbook(pb)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
