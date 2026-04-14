package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync"
)

// Adapter is the marker type for all adapters.
type Adapter any

// ZeroFactory should construct a zero-cost, zero-valued adapter.
type ZeroFactory func() Adapter

type Entry struct {
	ID            string
	ConstructZero ZeroFactory
}

type Node struct {
	Key             string
	AdapterID       string
	ItemName        string
	ResolvedWorkDir string

	AdapterMeta *MetaHeader
	ItemMeta    *MetaHeader

	Dependencies map[string]*Node
	Instance     Adapter
	Reused       bool
}

func (n *Node) Get(name string) (*Node, bool) {
	if n == nil || n.Dependencies == nil {
		return nil, false
	}
	child, ok := n.Dependencies[name]
	return child, ok
}

type Registry struct {
	mu          sync.RWMutex
	constructMu sync.Mutex
	entries     map[string]*Entry
	nodes       map[string]*Node
	searchMap   *SearchMap
}

var defaultRegistry = &Registry{
	entries: make(map[string]*Entry),
	nodes:   make(map[string]*Node),
}

// DefaultRegistry returns the package-global registry used by the helper funcs.
func DefaultRegistry() *Registry {
	return defaultRegistry
}

// SetSearchMap sets the SearchMap used by this registry.
// In typical CLI usage it's set once at startup; we don't worry about races here.
func (r *Registry) SetSearchMap(sm *SearchMap) {
	r.searchMap = sm
}

// SetSearchPath constructs a SearchMap rooted at the given path and installs it
// into this registry. It returns the created SearchMap.
func (r *Registry) SetSearchPath(root string) (*SearchMap, error) {
	sm, err := NewSearchMap(root)
	if err != nil {
		return nil, err
	}
	r.searchMap = sm
	return sm, nil
}

func (r *Registry) SearchPath() string {
	if r == nil || r.searchMap == nil {
		return ""
	}
	return r.searchMap.root
}

// Convenience: configure the default registry's search path.
func SetDefaultSearchPath(root string) (*SearchMap, error) {
	return defaultRegistry.SetSearchPath(root)
}

// (Optional) Lower-level convenience if you already built a SearchMap yourself.
func SetDefaultSearchMap(sm *SearchMap) {
	defaultRegistry.SetSearchMap(sm)
}

// Register adds an adapter entry to the registry.
func (r *Registry) Register(adapterID string, f ZeroFactory) {
	id := strings.ToLower(adapterID)

	r.mu.Lock()
	r.entries[id] = &Entry{
		ID:            adapterID,
		ConstructZero: f,
	}
	r.mu.Unlock()
}

// IsRegistered reports whether an adapterID has a registered factory.
func (r *Registry) IsRegistered(adapterID string) bool {
	r.mu.RLock()
	_, ok := r.entries[strings.ToLower(adapterID)]
	r.mu.RUnlock()
	return ok
}

func (r *Registry) getEntry(adapterID string) (*Entry, error) {
	r.mu.RLock()
	entry, ok := r.entries[strings.ToLower(adapterID)]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown adapter %q", adapterID)
	}
	return entry, nil
}

func keyGen(adapter Adapter, adapterID string, item *MetaHeader, workDir string) string {
	key := strings.ToLower(adapterID)

	if item != nil && item.Name != "" {
		key += "__" + item.Name
	}

	// Only workdir-discriminate if the adapter actually cares about workdir.
	if _, ok := adapter.(WorkDirSettable); !ok || workDir == "" {
		return key
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(workDir))

	return fmt.Sprintf("%s__%016x", key, h.Sum64())
}

func applyConfig(adapter Adapter, adapterID string, meta, itemMeta *MetaHeader) error {
	// Adapter-level config.
	if meta != nil && len(meta.RawSpec) > 0 {
		if configurable, ok := adapter.(Configurable); ok {
			Log().Debugf("setting config for adapter %s", adapterID)
			if err := json.Unmarshal(meta.RawSpec, configurable.ConfigPtr()); err != nil {
				return fmt.Errorf("decode %s spec: %w", adapterID, err)
			}
		}
	}

	// Item-level config overlay.
	if itemMeta != nil && len(itemMeta.RawSpec) > 0 {
		if itemConfigurable, ok := adapter.(ItemConfigurable); ok {
			Log().Debugf("setting item config for adapter %s", adapterID)
			if err := json.Unmarshal(itemMeta.RawSpec, itemConfigurable.ItemConfigPtr(itemMeta.Name)); err != nil {
				return fmt.Errorf("decode %s spec: %w", itemMeta.Name, err)
			}
		}
	}

	return nil
}

func resolveWorkDir(defaultWorkDir string, metas ...*MetaHeader) string {
	workDir := defaultWorkDir
	for _, m := range metas {
		if m == nil || m.WorkDir == "" {
			continue
		}
		workDir = m.WorkDir
	}
	return workDir
}

func debugAdapterInfo(zero Adapter, adapterID string, args ...string) {
	implements := []string{}
	if _, ok := zero.(Configurable); ok {
		implements = append(implements, "Configurable")
	}
	if _, ok := zero.(ItemConfigurable); ok {
		implements = append(implements, "ItemConfigurable")
	}
	if _, ok := zero.(Hydrater); ok {
		implements = append(implements, "Hydrater")
	}
	if _, ok := zero.(WorkDirSettable); ok {
		implements = append(implements, "WorkDirSettable")
	}
	Log().Debugf("request adapter %s (%s) %v\n", adapterID, strings.Join(implements, ","), args)
}

func (r *Registry) Construct(adapterID string, args ...string) (*Node, error) {
	// Construction is not a hot path for this kernel. A coarse lock keeps the
	// cache semantics easy to read: one top-level construct at a time, fully
	// build the node, then publish it into the cache on success.
	r.constructMu.Lock()
	defer r.constructMu.Unlock()

	return r.constructWithContext(adapterID, "", args...)
}

func (r *Registry) constructWithContext(adapterID string, defaultWorkDir string, args ...string) (*Node, error) {
	if r.searchMap == nil {
		return nil, fmt.Errorf("core: no SearchMap configured; call NewSearchMap first")
	}

	entry, err := r.getEntry(adapterID)
	if err != nil {
		return nil, err
	}

	zero := entry.ConstructZero()
	debugAdapterInfo(zero, adapterID, args...)

	var meta *MetaHeader
	var itemMeta *MetaHeader

	// Adapter-level config (optional).
	meta, err = r.searchMap.Load(adapterID, true)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed reading config for adapter %s: %w", adapterID, err)
	}

	// Item-level config (optional, if adapter supports it and a config arg is provided).
	if _, ok := zero.(ItemConfigurable); ok && len(args) > 0 {
		itemMeta, err = r.searchMap.Load(args[0], true)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed reading item config: %s for adapter %s: %w", args[0], adapterID, err)
		}
	}

	resolvedWorkDir := resolveWorkDir(defaultWorkDir, meta, itemMeta)
	regKey := keyGen(zero, adapterID, itemMeta, resolvedWorkDir)

	// Reuse an already-constructed node if present.
	r.mu.RLock()
	existing, ok := r.nodes[regKey]
	r.mu.RUnlock()
	if ok {
		existing.Reused = true
		Log().Debugf("reusing adapter: %s %v\n", adapterID, args)
		return existing, nil
	}

	node := &Node{
		Key:             regKey,
		AdapterID:       adapterID,
		ResolvedWorkDir: resolvedWorkDir,
		AdapterMeta:     meta,
		ItemMeta:        itemMeta,
		Dependencies:    make(map[string]*Node),
		Instance:        zero,
	}
	if itemMeta != nil {
		node.ItemName = itemMeta.Name
	}

	Log().Debugf("creating adapter node: %s %s %v\n", adapterID, regKey, args)

	r.mu.Lock()
	r.nodes[node.Key] = node
	r.mu.Unlock()

	if err := applyConfig(node.Instance, adapterID, meta, itemMeta); err != nil {
		return nil, err
	}

	if wdSetter, ok := node.Instance.(WorkDirSettable); ok && resolvedWorkDir != "" {
		Log().Debugf("setting working directory for adapter %s: %s\n", adapterID, resolvedWorkDir)
		wdSetter.SetWorkDir(resolvedWorkDir)
	}

	// Dependencies.
	if err := r.applyDeps(node, meta); err != nil {
		return nil, fmt.Errorf("dependency resolution for %s: %w", adapterID, err)
	}
	if err := r.applyDeps(node, itemMeta); err != nil {
		return nil, fmt.Errorf("dependency resolution for %s: %w", adapterID, err)
	}

	if err := validateRequiredDeps(node.Instance); err != nil {
		return nil, fmt.Errorf("validating adapter %s: %w", adapterID, err)
	}

	// Hydration hook.
	if hydrater, ok := node.Instance.(Hydrater); ok {
		Log().Debugf("hydrating adapter: %s\n", adapterID)
		if err := hydrater.Hydrate(context.Background()); err != nil {
			return nil, fmt.Errorf("hydrating adapter %s: %w", adapterID, err)
		}
	}

	return node, nil
}

// loadAllMetas is a small helper to retrieve all MetaHeaders for an adapter ID.
func (r *Registry) loadAllMetas(adapterID string) ([]*MetaHeader, error) {
	if r.searchMap == nil {
		return nil, fmt.Errorf("core: no SearchMap configured; call NewSearchMap first")
	}
	return r.searchMap.LoadAll(adapterID)
}

// Register adds an Adapter constructor to the global registry.
func Register(adapterID string, f ZeroFactory) {
	defaultRegistry.Register(adapterID, f)
}

func IsRegistered(adapterID string) bool {
	return defaultRegistry.IsRegistered(adapterID)
}

func Construct(adapterID string, args ...string) (*Node, error) {
	return defaultRegistry.Construct(adapterID, args...)
}

func NewAdapter(adapterID string, args ...string) (Adapter, error) {
	root, err := defaultRegistry.Construct(adapterID, args...)
	if err != nil {
		return nil, err
	}
	return root.Instance, nil
}

// NewAdapterAsFrom constructs an adapter from the given registry and asserts it implements T.
func NewAdapterAsFrom[T any](r *Registry, adapterID string, args ...string) (T, error) {
	var zeroT T

	root, err := r.Construct(adapterID, args...)
	if err != nil {
		return zeroT, err
	}

	t, ok := root.Instance.(T)
	if ok {
		return t, nil
	}

	return zeroT, fmt.Errorf(
		"adapter %q does not implement requested type: expected %T, got %T",
		adapterID, zeroT, root.Instance,
	)
}

// LoadAllAdaptersFrom loads all configured items for adapterID from the given registry
// and returns them as []T, skipping items that fail type assertion or construction.
func LoadAllAdaptersFrom[T any](r *Registry, adapterID string) ([]T, error) {
	metas, err := r.loadAllMetas(adapterID)
	if err != nil {
		return nil, err
	}

	var out []T
	for _, meta := range metas {
		t, err := NewAdapterAsFrom[T](r, adapterID, meta.Name)
		if err != nil {
			Log().Errorf("error: %v\n", err)
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func Adapters() map[string]Adapter {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()

	cp := make(map[string]Adapter, len(defaultRegistry.nodes))
	for k, n := range defaultRegistry.nodes {
		cp[k] = n.Instance
	}
	return cp
}

func Nodes() map[string]*Node {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()

	cp := make(map[string]*Node, len(defaultRegistry.nodes))
	for k, v := range defaultRegistry.nodes {
		cp[k] = v
	}
	return cp
}

// NewAdapterAs constructs an adapter from the default registry and asserts it implements T.
func NewAdapterAs[T any](adapterID string, args ...string) (T, error) {
	return NewAdapterAsFrom[T](defaultRegistry, adapterID, args...)
}

// LoadAllAdapters loads all configured items for adapterID from the default registry.
func LoadAllAdapters[T any](adapterID string) ([]T, error) {
	return LoadAllAdaptersFrom[T](defaultRegistry, adapterID)
}
