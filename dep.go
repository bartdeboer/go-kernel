package kernel

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/bartdeboer/words"
)

// applyDeps wires dependencies into a node's adapter instance and records the graph edges.
func (r *Registry) applyDeps(node *Node, meta *MetaHeader) error {
	if meta == nil {
		return nil
	}

	deps, err := resolvedDeps(node.Instance, meta)
	if err != nil {
		return err
	}
	if len(deps) == 0 {
		return nil
	}

	if err := r.resolveStructDeps(node, deps); err != nil {
		return err
	}
	return nil
}

// resolvedDeps combines explicit config deps with inferred struct deps.
// Config wins over inferred.
func resolvedDeps(adapter Adapter, meta *MetaHeader) (map[string]DepRef, error) {
	inferred, err := findStructDeps(adapter)
	if err != nil {
		return nil, err
	}

	var cfg map[string]DepRef
	if meta != nil {
		cfg = meta.Dependencies
	}

	return mergeDeps(cfg, inferred), nil
}

// resolveStructDeps initialises and assigns dependencies to exported
// struct fields on the parent node's instance whose names match deps' keys.
func (r *Registry) resolveStructDeps(parent *Node, deps map[string]DepRef) error {
	v := reflect.ValueOf(parent.Instance)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("resolveStructDeps: target must be a pointer, got %T", parent.Instance)
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("resolveStructDeps: target must point to a struct, got %T", parent.Instance)
	}

	for mapName, ref := range deps {
		fieldName := words.ToCapWords(mapName)

		field := v.FieldByName(fieldName)
		if !field.IsValid() {
			return fmt.Errorf("field %q not found in target struct", fieldName)
		}
		if !field.CanSet() {
			return fmt.Errorf("field %q is not settable", fieldName)
		}

		childArgs := append([]string(nil), ref.Args...)
		if ref.Name != "" {
			childArgs = append([]string{ref.Name}, childArgs...)
		}

		child, err := r.constructWithContext(ref.Adapter, parent.ResolvedWorkDir, childArgs...)
		if err != nil {
			return fmt.Errorf("dependency %q: %w", fieldName, err)
		}

		depVal := reflect.ValueOf(child.Instance)
		if !depVal.Type().AssignableTo(field.Type()) {
			return fmt.Errorf("dependency %q (%s) not assignable to field %s (%s)",
				fieldName, depVal.Type(), fieldName, field.Type())
		}

		Log().Debugf("assigned %s to %s %s\n",
			depVal.Type(), fieldName, field.Type())

		field.Set(depVal)
		parent.Dependencies[fieldName] = child
	}

	return nil
}

func validateRequiredDeps(target any) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("validateRequiredDeps: target must be a non-nil pointer to a struct, got %T", target)
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("validateRequiredDeps: target must point to a struct, got %T", target)
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		tag := fieldType.Tag.Get("core")
		if tag == "required" {
			if field.Kind() == reflect.Interface || field.Kind() == reflect.Ptr {
				if field.IsNil() {
					return fmt.Errorf("missing required dependency: field %q is nil", fieldType.Name)
				}
			} else {
				zero := reflect.Zero(field.Type())
				if reflect.DeepEqual(field.Interface(), zero.Interface()) {
					return fmt.Errorf("missing required dependency: field %q is not set", fieldType.Name)
				}
			}
		}
	}

	return nil
}

// findStructDeps inspects exported fields for `core` tags that specify an adapter id.
// It returns a deps map keyed by *field name*.
func findStructDeps(target any) (map[string]DepRef, error) {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil, fmt.Errorf("findStructDeps: target must be non-nil pointer, got %T", target)
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("findStructDeps: target must point to struct, got %T", target)
	}

	t := v.Type()
	out := make(map[string]DepRef)

	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		// Only exported fields participate in dependency injection.
		if sf.PkgPath != "" {
			continue
		}

		tag := strings.TrimSpace(sf.Tag.Get("core"))
		if tag == "" {
			continue
		}

		parts := strings.Split(tag, ",")

		var adapterID string

		switch {
		case len(parts) >= 2:
			// Positional form: first token is the adapter key (even if a later token is "required").
			adapterID = strings.TrimSpace(parts[0])
		default:
			// Single token form: either a flag (required) or an adapter key.
			p := strings.TrimSpace(parts[0])
			if !strings.EqualFold(p, "required") {
				adapterID = p
			}
		}

		if adapterID == "" {
			continue
		}

		out[sf.Name] = DepRef{Adapter: adapterID}
	}

	return out, nil
}

func mergeDeps(config map[string]DepRef, inferred map[string]DepRef) map[string]DepRef {
	if config == nil && inferred == nil {
		return nil
	}
	out := make(map[string]DepRef)

	// Config wins over inferred dependencies.
	for k, v := range config {
		out[k] = v
	}
	// Fill in any gaps from inferred struct tags.
	for k, v := range inferred {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}
