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

type Registry struct {
	mu          sync.RWMutex
	constructMu sync.Mutex
	entries     map[string]*Entry
	nodes       map[string]*Node
	searchMap   *SearchMap
}

func (r *Registry) SetSearchMap(sm *SearchMap) {
	r.searchMap = sm
}

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

func (r *Registry) Register(adapterID string, f ZeroFactory) {
	id := strings.ToLower(adapterID)

	r.mu.Lock()
	r.entries[id] = &Entry{
		ID:            adapterID,
		ConstructZero: f,
	}
	r.mu.Unlock()
}

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

func (r *Registry) NewNode(
	key string,
	adapterID string,
	instance Adapter,
	resolvedWorkDir string,
	adapterMeta, itemMeta *MetaHeader,
) *Node {
	node := &Node{
		Key:             key,
		AdapterID:       adapterID,
		ResolvedWorkDir: resolvedWorkDir,
		AdapterMeta:     adapterMeta,
		ItemMeta:        itemMeta,
		Dependencies:    make(map[string]*Node),
		Instance:        instance,
		reg:             r,
	}
	if itemMeta != nil {
		node.ItemName = itemMeta.Name
	}
	return node
}

func (r *Registry) loadMetas(adapterID string, instance Adapter, args ...string) (*MetaHeader, *MetaHeader, error) {
	if r.searchMap == nil {
		return nil, nil, fmt.Errorf("core: no SearchMap configured; call NewSearchMap first")
	}

	var meta *MetaHeader
	var itemMeta *MetaHeader
	var err error

	meta, err = r.searchMap.Load(adapterID, true)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("failed reading config for adapter %s: %w", adapterID, err)
	}

	if _, ok := instance.(ItemConfigurable); ok && len(args) > 0 {
		itemMeta, err = r.searchMap.Load(args[0], true)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("failed reading item config: %s for adapter %s: %w", args[0], adapterID, err)
		}
	}

	return meta, itemMeta, nil
}

func keyGen(adapter Adapter, adapterID string, item *MetaHeader, workDir string) string {
	key := strings.ToLower(adapterID)

	if item != nil && item.Name != "" {
		key += "__" + item.Name
	}

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
	if meta != nil && len(meta.RawSpec) > 0 {
		if configurable, ok := adapter.(Configurable); ok {
			Log().Debugf("setting config for adapter %s", adapterID)
			if err := json.Unmarshal(meta.RawSpec, configurable.ConfigPtr()); err != nil {
				return fmt.Errorf("decode %s spec: %w", adapterID, err)
			}
		}
	}

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

func debugAdapterInfo(instance Adapter, adapterID string, args ...string) {
	implements := []string{}
	if _, ok := instance.(Configurable); ok {
		implements = append(implements, "Configurable")
	}
	if _, ok := instance.(ItemConfigurable); ok {
		implements = append(implements, "ItemConfigurable")
	}
	if _, ok := instance.(Hydrater); ok {
		implements = append(implements, "Hydrater")
	}
	if _, ok := instance.(WorkDirSettable); ok {
		implements = append(implements, "WorkDirSettable")
	}
	Log().Debugf("request adapter %s (%s) %v\n", adapterID, strings.Join(implements, ","), args)
}

func (r *Registry) Construct(adapterID string, args ...string) (*Node, error) {
	r.constructMu.Lock()
	defer r.constructMu.Unlock()

	return r.constructWithWorkDir(adapterID, "", args...)
}

func (r *Registry) constructWithWorkDir(adapterID string, defaultWorkDir string, args ...string) (*Node, error) {
	if r.searchMap == nil {
		return nil, fmt.Errorf("core: no SearchMap configured; call NewSearchMap first")
	}

	entry, err := r.getEntry(adapterID)
	if err != nil {
		return nil, err
	}

	instance := entry.ConstructZero()
	debugAdapterInfo(instance, adapterID, args...)

	meta, itemMeta, err := r.loadMetas(adapterID, instance, args...)
	if err != nil {
		return nil, err
	}

	resolvedWorkDir := resolveWorkDir(defaultWorkDir, meta, itemMeta)
	regKey := keyGen(instance, adapterID, itemMeta, resolvedWorkDir)

	r.mu.RLock()
	existing, ok := r.nodes[regKey]
	r.mu.RUnlock()
	if ok {
		existing.Reused = true
		Log().Debugf("reusing adapter: %s %v\n", adapterID, args)
		return existing, nil
	}

	node := r.NewNode(regKey, adapterID, instance, resolvedWorkDir, meta, itemMeta)

	Log().Debugf("creating adapter node: %s %s %v\n", adapterID, regKey, args)

	r.mu.Lock()
	r.nodes[node.Key] = node
	r.mu.Unlock()

	if err := node.ApplyConfig(); err != nil {
		return nil, err
	}
	if err := node.ApplyWorkDir(); err != nil {
		return nil, err
	}
	if err := node.AssignDependencies(); err != nil {
		return nil, fmt.Errorf("dependency resolution for %s: %w", adapterID, err)
	}
	if err := node.Validate(); err != nil {
		return nil, fmt.Errorf("validating adapter %s: %w", adapterID, err)
	}
	if err := node.Hydrate(context.Background()); err != nil {
		return nil, fmt.Errorf("hydrating adapter %s: %w", adapterID, err)
	}

	return node, nil
}

func (r *Registry) loadAllMetas(adapterID string) ([]*MetaHeader, error) {
	if r.searchMap == nil {
		return nil, fmt.Errorf("core: no SearchMap configured; call NewSearchMap first")
	}
	return r.searchMap.LoadAll(adapterID)
}

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
