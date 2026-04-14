package kernel

var defaultRegistry = &Registry{
	entries: make(map[string]*Entry),
	nodes:   make(map[string]*Node),
}

func DefaultRegistry() *Registry {
	return defaultRegistry
}

func SetDefaultSearchPath(root string) (*SearchMap, error) {
	return defaultRegistry.SetSearchPath(root)
}

func SetDefaultSearchMap(sm *SearchMap) {
	defaultRegistry.SetSearchMap(sm)
}

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

func NewAdapterAs[T any](adapterID string, args ...string) (T, error) {
	return NewAdapterAsFrom[T](defaultRegistry, adapterID, args...)
}

func LoadAllAdapters[T any](adapterID string) ([]T, error) {
	return LoadAllAdaptersFrom[T](defaultRegistry, adapterID)
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
