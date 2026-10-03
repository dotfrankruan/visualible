// Package artifact turns Visualible documents into portable files.
//
// Product principle: user automation belongs to the user. Anything
// authored in Visualible must be exportable as standard Ansible
// artifacts that run without Visualible, so exports contain only what
// Ansible itself understands — never credentials or Visualible-only
// metadata.
package artifact

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/dotfrankruan/visualible/internal/ir"
	"github.com/dotfrankruan/visualible/internal/render"
)

// Artifact is a ready-to-download file.
type Artifact struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Exporter converts a document into a downloadable artifact. Additional
// exporters (project bundle ZIP, Git repository, S3 artifact, role
// export) can implement this interface later without touching callers.
type Exporter interface {
	ID() string
	ExportPlaybook(pb *ir.Playbook) (*Artifact, error)
}

// YAMLExporter writes a single playbook as standard Ansible YAML.
type YAMLExporter struct{}

// ID identifies the exporter.
func (YAMLExporter) ID() string { return "yaml" }

// ExportPlaybook renders the playbook with the canonical renderer. The
// output is ordinary Ansible YAML: no Visualible metadata is added, and
// credentials (which live in the credential store, not in the playbook)
// can never appear.
func (YAMLExporter) ExportPlaybook(pb *ir.Playbook) (*Artifact, error) {
	if pb == nil {
		return nil, fmt.Errorf("no playbook to export")
	}
	data, err := render.Playbook(pb)
	if err != nil {
		return nil, err
	}
	return &Artifact{
		Filename:    PlaybookFilename(pb.Name, "yml"),
		ContentType: "application/yaml; charset=utf-8",
		Data:        data,
	}, nil
}

// PlaybookFilename builds a safe, human filename from a document name:
// "Configure nginx" -> "configure-nginx.yml". Unicode letters are kept,
// unsafe filesystem characters are replaced, and empty or reserved-only
// names fall back to a stable default.
func PlaybookFilename(name, ext string) string {
	slug := Slug(name)
	if slug == "" {
		slug = "playbook"
	}
	return slug + "." + ext
}

// Slug converts a display name into a filesystem-safe identifier.
func Slug(name string) string {
	var b strings.Builder
	lastDash := true // avoid leading dashes
	for _, r := range strings.TrimSpace(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// Keep unicode letters and digits as-is; they are valid in
			// modern filesystems and preserve non-English names.
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r) || r == '.':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			// Unsafe filesystem characters (/, \, :, *, ?, ", <, >, |, …)
			// and everything else collapse into a single separator.
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() > 80 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	// Windows reserved device names would be unusable as-is.
	switch strings.ToUpper(slug) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		slug += "-file"
	}
	return slug
}

// UniqueFilename makes a filename distinct within a set of taken names by
// appending a counter: "nginx.yml", "nginx-2.yml", "nginx-3.yml".
func UniqueFilename(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	stem, ext := base, ""
	if i := strings.LastIndex(base, "."); i > 0 {
		stem, ext = base[:i], base[i:]
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, n, ext)
		if !taken[candidate] {
			return candidate
		}
	}
}
