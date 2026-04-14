package kernel

import (
	"context"
	"fmt"
	"strings"
)

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

	reg *Registry
}

func (n *Node) Registry() *Registry {
	if n == nil {
		return nil
	}
	return n.reg
}

func (n *Node) Get(name string) (*Node, bool) {
	if n == nil || n.Dependencies == nil {
		return nil, false
	}
	child, ok := n.Dependencies[name]
	return child, ok
}

func (n *Node) AttachDependency(name string, child *Node) {
	if n.Dependencies == nil {
		n.Dependencies = make(map[string]*Node)
	}
	n.Dependencies[name] = child
}

func (n *Node) ApplyConfig() error {
	return applyConfig(n.Instance, n.AdapterID, n.AdapterMeta, n.ItemMeta)
}

func (n *Node) ApplyWorkDir() error {
	if n.ResolvedWorkDir == "" {
		return nil
	}

	wdSetter, ok := n.Instance.(WorkDirSettable)
	if !ok {
		return nil
	}

	Log().Debugf("setting working directory for adapter %s: %s\n", n.AdapterID, n.ResolvedWorkDir)
	wdSetter.SetWorkDir(n.ResolvedWorkDir)
	return nil
}

func (n *Node) Validate() error {
	return validateRequiredDeps(n.Instance)
}

func (n *Node) Hydrate(ctx context.Context) error {
	hydrater, ok := n.Instance.(Hydrater)
	if !ok {
		return nil
	}

	Log().Debugf("hydrating adapter: %s\n", n.AdapterID)
	return hydrater.Hydrate(ctx)
}

func (n *Node) ConstructDependency(adapterID string, args ...string) (*Node, error) {
	if n == nil || n.reg == nil {
		return nil, fmt.Errorf("node has no registry")
	}
	return n.reg.constructWithWorkDir(adapterID, n.ResolvedWorkDir, args...)
}

func (n *Node) ConfigSourceNames() []string {
	if n == nil {
		return nil
	}

	var out []string
	if n.ItemMeta != nil && n.ItemMeta.SourceName != "" {
		out = append(out, n.ItemMeta.SourceName)
	}
	if n.AdapterMeta != nil && n.AdapterMeta.SourceName != "" {
		if n.ItemMeta == nil || n.AdapterMeta.SourceName != n.ItemMeta.SourceName {
			out = append(out, n.AdapterMeta.SourceName)
		}
	}
	return out
}

func (n *Node) Attributes() string {
	if n == nil {
		return ""
	}

	parts := []string{}

	if n.ItemName != "" {
		parts = append(parts, "name="+n.ItemName)
	}

	for _, src := range n.ConfigSourceNames() {
		parts = append(parts, "cfg="+src)
	}

	if n.ResolvedWorkDir != "" {
		parts = append(parts, "context="+n.ResolvedWorkDir)
	}

	return strings.Join(parts, ", ")
}
