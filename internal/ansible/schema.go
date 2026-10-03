package ansible

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ModuleSummary is one entry of the module catalog (ansible-doc -l -j).
type ModuleSummary struct {
	FQCN             string `json:"fqcn"`
	Name             string `json:"name"`
	Collection       string `json:"collection"`
	ShortDescription string `json:"shortDescription"`
}

// ModuleSchema is the normalized, frontend-ready description of one
// Ansible module. It carries everything needed to build dynamic forms.
type ModuleSchema struct {
	FQCN             string                   `json:"fqcn"`
	Name             string                   `json:"name"`
	Collection       string                   `json:"collection"`
	ShortDescription string                   `json:"shortDescription"`
	Description      []string                 `json:"description,omitempty"`
	VersionAdded     string                   `json:"versionAdded,omitempty"`
	Requirements     []string                 `json:"requirements,omitempty"`
	Notes            []string                 `json:"notes,omitempty"`
	SeeAlso          []string                 `json:"seeAlso,omitempty"`
	Options          map[string]*OptionSchema `json:"options,omitempty"`
	Examples         string                   `json:"examples,omitempty"`
}

// OptionSchema describes one module argument, recursively via Suboptions.
type OptionSchema struct {
	Name         string                   `json:"name"`
	Type         string                   `json:"type"` // str, int, float, bool, list, dict, path, raw, ...
	Description  []string                 `json:"description,omitempty"`
	Required     bool                     `json:"required,omitempty"`
	Default      any                      `json:"default,omitempty"`
	Choices      []any                    `json:"choices,omitempty"`
	Aliases      []string                 `json:"aliases,omitempty"`
	Elements     string                   `json:"elements,omitempty"` // element type for lists
	Suboptions   map[string]*OptionSchema `json:"suboptions,omitempty"`
	VersionAdded string                   `json:"versionAdded,omitempty"`
}

// --- Raw ansible-doc JSON shapes (parsed defensively) ---

// strList accepts either a single string or a list of strings, which both
// occur in ansible-doc output across versions.
type strList []string

func (s *strList) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '"' {
		var single string
		if err := json.Unmarshal(b, &single); err != nil {
			return err
		}
		*s = strList{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*s = list
	return nil
}

// flexString accepts fields that ansible-doc emits either as strings or
// as raw JSON numbers (version_added is a number in many real modules,
// e.g. apt's auto_install_module_deps: 2.19). Raw number literals are
// kept verbatim to avoid float reformatting.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	// Number (or other scalar literal): keep the raw representation.
	*f = flexString(string(b))
	return nil
}

type rawDoc struct {
	Collection       string                     `json:"collection"`
	Module           string                     `json:"module"`
	ShortDescription string                     `json:"short_description"`
	Description      strList                    `json:"description"`
	VersionAdded     flexString                 `json:"version_added"`
	Requirements     strList                    `json:"requirements"`
	Notes            strList                    `json:"notes"`
	SeeAlso          []rawSeeAlso               `json:"seealso"`
	Options          map[string]json.RawMessage `json:"options"`
}

type rawSeeAlso struct {
	Module      string `json:"module"`
	Name        string `json:"name"`
	Link        string `json:"link"`
	Description string `json:"description"`
	Ref         string `json:"ref"`
}

type rawEntry struct {
	Doc      *rawDoc `json:"doc"`
	Examples string  `json:"examples"`
}

// choicesList accepts choices as a list (common) or as an object mapping
// each choice to its human description (real: ansible.builtin.dnf's
// use_backend). Objects are flattened to their keys, sorted for
// determinism.
type choicesList []any

func (c *choicesList) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '{' {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(choicesList, 0, len(keys))
		for _, k := range keys {
			out = append(out, k)
		}
		*c = out
		return nil
	}
	var list []any
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*c = list
	return nil
}

// rawOption mirrors one option. Suboptions may appear either nested under
// "suboptions" or inlined as option keys (older formats); we support both.
type rawOption struct {
	Type         string                     `json:"type"`
	Description  strList                    `json:"description"`
	Required     bool                       `json:"required"`
	Default      any                        `json:"default"`
	Choices      choicesList                `json:"choices"`
	Aliases      strList                    `json:"aliases"`
	Elements     string                     `json:"elements"`
	Suboptions   map[string]json.RawMessage `json:"suboptions"`
	VersionAdded flexString                 `json:"version_added"`
	Options      map[string]json.RawMessage `json:"options"` // alternate nesting
}

// NormalizeModuleList parses `ansible-doc -l -j` output into summaries.
// The real ansible-core 2.21 shape is a flat map of FQCN to short
// description string; older/other shapes use an object with a doc
// section. Both are accepted.
func NormalizeModuleList(data []byte) ([]ModuleSummary, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse module list: %w", err)
	}
	out := make([]ModuleSummary, 0, len(raw))
	for fqcn, blob := range raw {
		b := bytes.TrimSpace(blob)
		// Real shape: bare description string.
		if len(b) > 0 && b[0] == '"' {
			var desc string
			if err := json.Unmarshal(b, &desc); err != nil {
				continue
			}
			out = append(out, ModuleSummary{
				FQCN:             fqcn,
				Name:             shortName(fqcn),
				Collection:       collectionOf(fqcn),
				ShortDescription: desc,
			})
			continue
		}
		// Object shape with doc section.
		var entry rawEntry
		if err := json.Unmarshal(b, &entry); err != nil || entry.Doc == nil {
			continue // tolerate unexpected entry shapes
		}
		name := entry.Doc.Module
		if name == "" {
			name = shortName(fqcn)
		}
		collection := entry.Doc.Collection
		if collection == "" {
			collection = collectionOf(fqcn)
		}
		out = append(out, ModuleSummary{
			FQCN:             fqcn,
			Name:             name,
			Collection:       collection,
			ShortDescription: entry.Doc.ShortDescription,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FQCN < out[j].FQCN })
	return out, nil
}

// collectionOf derives the collection ("community.docker") from an FQCN
// ("community.docker.docker_container").
func collectionOf(fqcn string) string {
	parts := strings.SplitN(fqcn, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// NormalizeModuleDoc parses `ansible-doc -j <fqcn>` output into a schema.
func NormalizeModuleDoc(data []byte) (*ModuleSchema, error) {
	var raw map[string]rawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse module doc: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("parse module doc: empty document")
	}
	// The document is keyed by FQCN; there is exactly one entry.
	var fqcn string
	var entry rawEntry
	for k, v := range raw {
		fqcn, entry = k, v
	}
	if entry.Doc == nil {
		return nil, fmt.Errorf("parse module doc for %q: missing doc section", fqcn)
	}
	d := entry.Doc
	name := d.Module
	if name == "" {
		name = shortName(fqcn)
	}
	schema := &ModuleSchema{
		FQCN:             fqcn,
		Name:             name,
		Collection:       d.Collection,
		ShortDescription: d.ShortDescription,
		Description:      d.Description,
		VersionAdded:     string(d.VersionAdded),
		Requirements:     d.Requirements,
		Notes:            d.Notes,
		Examples:         entry.Examples,
	}
	for _, sa := range d.SeeAlso {
		switch {
		case sa.Module != "":
			schema.SeeAlso = append(schema.SeeAlso, sa.Module)
		case sa.Link != "":
			schema.SeeAlso = append(schema.SeeAlso, sa.Link)
		case sa.Name != "":
			schema.SeeAlso = append(schema.SeeAlso, sa.Name)
		}
	}
	if len(d.Options) > 0 {
		opts, err := normalizeOptions(d.Options)
		if err != nil {
			return nil, fmt.Errorf("parse module doc for %q: %w", fqcn, err)
		}
		schema.Options = opts
	}
	return schema, nil
}

func normalizeOptions(raw map[string]json.RawMessage) (map[string]*OptionSchema, error) {
	out := make(map[string]*OptionSchema, len(raw))
	for name, blob := range raw {
		opt, err := normalizeOption(name, blob)
		if err != nil {
			return nil, err
		}
		out[name] = opt
	}
	return out, nil
}

func normalizeOption(name string, blob json.RawMessage) (*OptionSchema, error) {
	var ro rawOption
	if err := json.Unmarshal(blob, &ro); err != nil {
		return nil, fmt.Errorf("option %q: %w", name, err)
	}
	opt := &OptionSchema{
		Name:         name,
		Type:         ro.Type,
		Description:  ro.Description,
		Required:     ro.Required,
		Default:      ro.Default,
		Choices:      []any(ro.Choices),
		Aliases:      ro.Aliases,
		Elements:     ro.Elements,
		VersionAdded: string(ro.VersionAdded),
	}
	if opt.Type == "" {
		opt.Type = "raw"
	}
	subs := ro.Suboptions
	if len(subs) == 0 {
		subs = ro.Options
	}
	if len(subs) > 0 {
		nested, err := normalizeOptions(subs)
		if err != nil {
			return nil, err
		}
		opt.Suboptions = nested
	}
	return opt, nil
}

func shortName(fqcn string) string {
	if i := strings.LastIndex(fqcn, "."); i >= 0 {
		return fqcn[i+1:]
	}
	return fqcn
}
