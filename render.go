package kernel

import (
	"sort"
	"strings"
)

func (n *Node) String() string {
	if n == nil {
		return "<nil>"
	}

	var b strings.Builder
	renderNode(&b, n, "", true, map[string]bool{})
	return b.String()
}

func renderNode(b *strings.Builder, n *Node, prefix string, last bool, seen map[string]bool) {
	connector := "├── "
	nextPrefix := prefix + "│   "
	if last {
		connector = "└── "
		nextPrefix = prefix + "    "
	}

	label := n.AdapterID
	if attrs := attributesForNode(n); attrs != "" {
		label += " <" + attrs + ">"
	}
	if n.Reused {
		label += " (reused)"
	}

	if prefix == "" {
		b.WriteString(label)
		b.WriteByte('\n')
	} else {
		b.WriteString(prefix)
		b.WriteString(connector)
		b.WriteString(label)
		b.WriteByte('\n')
	}

	if seen[n.Key] {
		return
	}
	seen[n.Key] = true

	names := make([]string, 0, len(n.Dependencies))
	for name := range n.Dependencies {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		child := n.Dependencies[name]
		isLast := i == len(names)-1

		if isLast {
			b.WriteString(nextPrefix)
			b.WriteString("└── ")
		} else {
			b.WriteString(nextPrefix)
			b.WriteString("├── ")
		}
		b.WriteString(name)
		b.WriteByte('\n')

		childPrefix := nextPrefix
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}

		renderNode(b, child, childPrefix, true, seen)
	}
}

func attributesForNode(n *Node) string {
	if n == nil {
		return ""
	}

	parts := []string{}

	if n.ItemName != "" {
		parts = append(parts, "name="+n.ItemName)
	}

	if n.ItemMeta != nil && n.ItemMeta.SourceName != "" {
		parts = append(parts, "cfg="+n.ItemMeta.SourceName)
	}

	if n.AdapterMeta != nil && n.AdapterMeta.SourceName != "" {
		if n.ItemMeta == nil || n.AdapterMeta.SourceName != n.ItemMeta.SourceName {
			parts = append(parts, "cfg="+n.AdapterMeta.SourceName)
		}
	}

	if n.ResolvedWorkDir != "" {
		parts = append(parts, "context="+n.ResolvedWorkDir)
	}

	return strings.Join(parts, ", ")
}
