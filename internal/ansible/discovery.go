package ansible

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/dotfrankruan/visualible/internal/logging"
)

// Cache persists normalized module metadata on disk so startup does not
// require re-running ansible-doc for every module. The cache is advisory:
// misses simply fall through to live ansible-doc calls.
type Cache struct {
	dir string

	mu      sync.RWMutex
	list    []ModuleSummary
	listAt  time.Time
	schemas map[string]*cachedSchema
}

type cachedSchema struct {
	Schema    *ModuleSchema `json:"schema"`
	FetchedAt time.Time     `json:"fetchedAt"`
}

type cacheFile struct {
	Version int                      `json:"version"`
	List    []ModuleSummary          `json:"list"`
	ListAt  time.Time                `json:"listAt"`
	Schemas map[string]*cachedSchema `json:"schemas"`
}

const cacheVersion = 1

// OpenCache loads (or creates) a cache rooted at dir.
func OpenCache(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	c := &Cache{dir: dir, schemas: map[string]*cachedSchema{}}
	data, err := os.ReadFile(c.path())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cache: %w", err)
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		// Corrupt cache is not fatal; start empty.
		return c, nil
	}
	if f.Version == cacheVersion {
		c.list = f.List
		c.listAt = f.ListAt
		if f.Schemas != nil {
			c.schemas = f.Schemas
		}
	}
	return c, nil
}

func (c *Cache) path() string { return filepath.Join(c.dir, "modules.json") }

// List returns the cached module catalog, if any.
func (c *Cache) List() ([]ModuleSummary, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.list == nil {
		return nil, false
	}
	out := make([]ModuleSummary, len(c.list))
	copy(out, c.list)
	return out, true
}

// Schema returns a cached module schema.
func (c *Cache) Schema(fqcn string) (*ModuleSchema, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.schemas[fqcn]
	if !ok {
		return nil, false
	}
	return s.Schema, true
}

// StoreList replaces the cached catalog and persists it.
func (c *Cache) StoreList(list []ModuleSummary) error {
	c.mu.Lock()
	c.list = list
	c.listAt = time.Now()
	c.mu.Unlock()
	return c.save()
}

// StoreSchema caches one schema and persists it.
func (c *Cache) StoreSchema(s *ModuleSchema) error {
	c.mu.Lock()
	c.schemas[s.FQCN] = &cachedSchema{Schema: s, FetchedAt: time.Now()}
	c.mu.Unlock()
	return c.save()
}

func (c *Cache) save() error {
	c.mu.RLock()
	f := cacheFile{Version: cacheVersion, List: c.list, ListAt: c.listAt, Schemas: c.schemas}
	c.mu.RUnlock()
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp := c.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path())
}

// Discovery combines detection, ansible-doc access and caching into the
// service the HTTP layer uses. When Ansible is absent, every method
// reports ErrNotFound and the UI can show a clear setup state.
type Discovery struct {
	inst  *Installation
	doc   *DocClient
	cache *Cache
}

// NewDiscovery detects Ansible and opens the cache. A nil installation
// (Ansible missing) is a valid, explicitly-represented state.
func NewDiscovery(ctx context.Context, cacheDir string) (*Discovery, error) {
	cache, err := OpenCache(cacheDir)
	if err != nil {
		return nil, err
	}
	d := &Discovery{cache: cache}
	inst, err := Detect(ctx)
	if err == nil {
		d.inst = inst
		d.doc = NewDocClient(inst)
		logging.Info("ansible installation detected",
			"version", inst.Version, "path", inst.Path,
			"ansibleDoc", inst.AnsibleDocPath, "ansiblePlaybook", inst.PlaybookPath)
	} else {
		logging.Warn("ansible was not found on PATH", "err", err)
	}
	return d, nil
}

// NewDiscoveryWith injects dependencies for tests.
func NewDiscoveryWith(inst *Installation, doc *DocClient, cache *Cache) *Discovery {
	return &Discovery{inst: inst, doc: doc, cache: cache}
}

// ApplyOverrides re-detects Ansible honoring explicit path overrides from
// settings. All-empty overrides fall back to PATH lookup. A nonexistent
// override path marks Ansible unavailable (explicit is better than a
// silently wrong guess).
func (d *Discovery) ApplyOverrides(ctx context.Context, ansiblePath, docPath, playbookPath string) {
	d.inst = nil
	d.doc = nil
	if ansiblePath == "" && docPath == "" && playbookPath == "" {
		if inst, err := Detect(ctx); err == nil {
			d.inst = inst
			d.doc = NewDocClient(inst)
		}
		return
	}
	inst := &Installation{Path: ansiblePath, AnsibleDocPath: docPath, PlaybookPath: playbookPath}
	if inst.Path == "" {
		inst.Path, _ = exec.LookPath("ansible")
	}
	if inst.AnsibleDocPath == "" {
		inst.AnsibleDocPath, _ = exec.LookPath("ansible-doc")
	}
	if inst.PlaybookPath == "" {
		inst.PlaybookPath, _ = exec.LookPath("ansible-playbook")
	}
	if inst.Path == "" || inst.AnsibleDocPath == "" || inst.PlaybookPath == "" {
		return
	}
	for _, p := range []string{inst.Path, inst.AnsibleDocPath, inst.PlaybookPath} {
		if _, err := os.Stat(p); err != nil {
			return
		}
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if out, err := defaultOutput(vctx, inst.Path, "--version"); err == nil {
		inst.Version = parseVersion(string(out))
	}
	d.inst = inst
	d.doc = NewDocClient(inst)
}

// Status describes Ansible availability for the UI.
type Status struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path,omitempty"`
}

// Status reports whether Ansible was detected.
func (d *Discovery) Status() Status {
	if d.inst == nil {
		return Status{Available: false}
	}
	return Status{Available: true, Version: d.inst.Version, Path: d.inst.Path}
}

// Installation returns the detected installation, or nil.
func (d *Discovery) Installation() *Installation { return d.inst }

// CachedModules returns the cached catalog without triggering discovery.
func (d *Discovery) CachedModules() ([]ModuleSummary, bool) {
	return d.cache.List()
}

// Modules returns the module catalog, refreshing from ansible-doc when the
// cache is empty. Pass refresh=true to force re-discovery.
func (d *Discovery) Modules(ctx context.Context, refresh bool) ([]ModuleSummary, error) {
	if !refresh {
		if list, ok := d.cache.List(); ok {
			return list, nil
		}
	}
	if d.doc == nil {
		return nil, ErrNotFound
	}
	start := time.Now()
	raw, err := d.doc.ListJSON(ctx)
	if err != nil {
		// Fall back to a stale cache rather than failing hard.
		if list, ok := d.cache.List(); ok {
			logging.Warn("module discovery failed; serving cached catalog",
				"cached", len(list), "err", err)
			return list, nil
		}
		return nil, err
	}
	list, err := NormalizeModuleList(raw)
	if err != nil {
		logging.Error("could not normalize the module list", "err", err)
		return nil, err
	}
	if err := d.cache.StoreList(list); err != nil {
		logging.Warn("could not persist the module cache", "err", err)
	}
	logging.Info("module discovery finished",
		"modules", len(list),
		"duration", time.Since(start).Round(time.Millisecond))
	return list, nil
}

// Module returns the full schema for one module, using the cache first.
func (d *Discovery) Module(ctx context.Context, fqcn string, refresh bool) (*ModuleSchema, error) {
	if err := ValidateFQCN(fqcn); err != nil {
		return nil, err
	}
	if !refresh {
		if s, ok := d.cache.Schema(fqcn); ok {
			return s, nil
		}
	}
	if d.doc == nil {
		return nil, ErrNotFound
	}
	raw, err := d.doc.DocJSON(ctx, fqcn)
	if err != nil {
		if s, ok := d.cache.Schema(fqcn); ok {
			return s, nil
		}
		return nil, err
	}
	schema, err := NormalizeModuleDoc(raw)
	if err != nil {
		logging.Warn("could not normalize module documentation", "module", fqcn, "err", err)
		return nil, err
	}
	if err := d.cache.StoreSchema(schema); err != nil {
		logging.Warn("could not persist the module schema cache", "module", fqcn, "err", err)
	}
	logging.Debug("module schema loaded",
		"module", fqcn, "options", len(schema.Options), "collection", schema.Collection)
	return schema, nil
}
