package kernel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	// workDirMap overrides work directories by name, filled from CORE_WORK_DIR_MAP env.
	workDirMap = map[string]string{}
)

type DepRef struct {
	Adapter string   `json:"adapter"`        // required
	Name    string   `json:"name,omitempty"` // fallback on config name
	Args    []string `json:"args,omitempty"` // extra CLI-style args

	// Optional custom override for the dependency's context.
	// Context string `json:"context,omitempty"`
}

// The Context is managed by the system to ensure those paths are adjusted
// accordingly when the system runs in a container (with volume mounts).
// Adapters still need to use the value manually to use the context.
type MetaHeader struct {
	Name         string            `json:"name"` // fallback on filename
	APIVersion   string            `json:"api_version"`
	Adapter      string            `json:"adapter,omitempty"`
	Dependencies map[string]DepRef `json:"dependencies"`
	RawSpec      json.RawMessage   `json:"spec"`     // adapter-specific payload
	WorkDir      string            `json:"work_dir"` // project path (rel or abs)
	SourcePath   string            `json:"-"`
	SourceName   string            `json:"-"`
}

type SearchMap struct {
	root  string
	fs    FileSystem
	Short map[string][]string // basename (no .json) -> []absolute paths
	Full  map[string]string   // relative/key (no .json) -> absolute path
}

func init() {
	workDirJSON := os.Getenv("CORE_WORK_DIR_MAP")
	if workDirJSON != "" {
		_ = json.Unmarshal([]byte(workDirJSON), &workDirMap)
	}
}

func NewSearchMapWithFS(root string, fsys FileSystem) (*SearchMap, error) {
	sm := &SearchMap{
		root:  root,
		fs:    fsys,
		Short: make(map[string][]string),
		Full:  make(map[string]string),
	}

	err := fsys.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".json" {
			return nil
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve abs path %q: %w", path, err)
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativize %q: %w", path, err)
		}
		relKey := strings.TrimSuffix(rel, ".json")
		sm.Full[relKey] = absPath

		shortKey := strings.TrimSuffix(d.Name(), ".json")
		sm.Short[shortKey] = append(sm.Short[shortKey], absPath)
		return nil
	})
	if err != nil {
		return sm, err
	}
	return sm, nil
}

// Thin wrapper using osFS.
func NewSearchMap(root string) (*SearchMap, error) {
	return NewSearchMapWithFS(root, osFS{})
}

// Resolve finds the one absolute path for name.
// name can be either the short key ("dev") or full key ("env/dev").
func (sm *SearchMap) Resolve(name string) (string, error) {
	// Try full-key first
	if p, ok := sm.Full[name]; ok {
		return p, nil
	}

	// Then short-key
	list, ok := sm.Short[name]
	if !ok || len(list) == 0 {
		return "", os.ErrNotExist
	}
	if len(list) > 1 {
		return "", fmt.Errorf(
			"ambiguous config %q matches:\n  - %s",
			name, strings.Join(list, "\n  - "),
		)
	}
	return list[0], nil
}

// Load locates, reads, unmarshals and post-processes a MetaHeader.
// Should ensure MetaHeader.Name is set.
func (sm *SearchMap) Load(name string, verbose bool) (*MetaHeader, error) {
	cfgPath, err := sm.Resolve(name)
	if err != nil {
		return nil, err
	}

	if verbose {
		Log().Debugf("reading %s config: %s\n", name, cfgPath)
	}

	data, err := sm.fs.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfgPath, err)
	}

	var h MetaHeader
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("decode %s: %w", cfgPath, err)
	}

	if strings.TrimSpace(h.Name) == "" {
		h.Name = strings.TrimSuffix(filepath.Base(cfgPath), ".json")
	}
	h.SourcePath = cfgPath
	h.SourceName = cfgPath

	if absRoot, err := filepath.Abs(sm.root); err == nil {
		if relPath, err := filepath.Rel(absRoot, cfgPath); err == nil && !strings.HasPrefix(relPath, "..") {
			h.SourceName = filepath.ToSlash(relPath)
		}
	}

	// Override from env/contextMap if present
	if envWorkDir, ok := workDirMap[h.Name]; ok {
		h.WorkDir = filepath.Clean(envWorkDir)
	}

	// Make Context absolute if it’s relative
	if h.WorkDir != "" && !filepath.IsAbs(h.WorkDir) {
		dir := filepath.Dir(cfgPath)
		absWorkDir, err := filepath.Abs(filepath.Join(dir, h.WorkDir))
		if err != nil {
			return nil, fmt.Errorf("resolve context %q: %w", h.WorkDir, err)
		}
		h.WorkDir = filepath.Clean(absWorkDir)
	}

	return &h, nil
}

// LoadAll walks through every indexed config, loads it, and
// returns those whose Adapter matches adapterID (or all if adapterID=="").
func (sm *SearchMap) LoadAll(adapterID string) ([]*MetaHeader, error) {
	// Collect keys in deterministic order
	keys := make([]string, 0, len(sm.Full))
	for k := range sm.Full {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var result []*MetaHeader
	for _, key := range keys {
		meta, err := sm.Load(key, false)
		if errors.Is(err, os.ErrNotExist) {
			Log().Infof("could not find config for: %s\n", key)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("error loading meta %q: %w", key, err)
		}
		if adapterID != "" && !strings.EqualFold(meta.Adapter, adapterID) {
			continue
		}
		result = append(result, meta)
	}
	return result, nil
}

// LoadAll is a convenience function that uses the default registry's SearchMap.
func LoadAll(adapterID string) ([]*MetaHeader, error) {
	sm := defaultRegistry.searchMap
	if sm == nil {
		return nil, fmt.Errorf("core: no SearchMap configured; call NewSearchMap first")
	}
	return sm.LoadAll(adapterID)
}
