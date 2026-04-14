package kernel

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/bartdeboer/words"
)

func dependencyTag(sf reflect.StructField) string {
	if tag := strings.TrimSpace(sf.Tag.Get("kernel")); tag != "" {
		return tag
	}
	return strings.TrimSpace(sf.Tag.Get("core"))
}

func metaDependencies(meta *MetaHeader) map[string]DepRef {
	if meta == nil {
		return nil
	}
	return meta.Dependencies
}

func normalizeDepKeys(deps map[string]DepRef) map[string]DepRef {
	if deps == nil {
		return nil
	}

	out := make(map[string]DepRef, len(deps))
	for k, v := range deps {
		out[words.ToCapWords(k)] = v
	}
	return out
}

func (n *Node) AssignDependencies() error {
	if n == nil {
		return fmt.Errorf("assign dependencies: nil node")
	}
	if n.reg == nil {
		return fmt.Errorf("assign dependencies: node has no registry")
	}

	inferred, err := findStructDeps(n.Instance)
	if err != nil {
		return err
	}

	deps := mergeDeps(normalizeDepKeys(metaDependencies(n.AdapterMeta)), inferred)
	deps = mergeDeps(normalizeDepKeys(metaDependencies(n.ItemMeta)), deps)

	if len(deps) == 0 {
		return nil
	}

	v := reflect.ValueOf(n.Instance)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("assign dependencies: target must be a pointer, got %T", n.Instance)
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("assign dependencies: target must point to a struct, got %T", n.Instance)
	}

	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, fieldName := range names {
		depRef := deps[fieldName]

		field := v.FieldByName(fieldName)
		if !field.IsValid() {
			return fmt.Errorf("field %q not found in target struct", fieldName)
		}
		if !field.CanSet() {
			return fmt.Errorf("field %q is not settable", fieldName)
		}

		childArgs := append([]string(nil), depRef.Args...)
		if depRef.Name != "" {
			childArgs = append([]string{depRef.Name}, childArgs...)
		}

		child, err := n.reg.constructWithWorkDir(depRef.Adapter, n.ResolvedWorkDir, childArgs...)
		if err != nil {
			return fmt.Errorf("dependency %q: %w", fieldName, err)
		}

		depVal := reflect.ValueOf(child.Instance)
		if !depVal.Type().AssignableTo(field.Type()) {
			return fmt.Errorf(
				"dependency %q (%s) not assignable to field %s (%s)",
				fieldName, depVal.Type(), fieldName, field.Type(),
			)
		}

		Log().Debugf("assigned %s to %s %s\n", depVal.Type(), fieldName, field.Type())

		field.Set(depVal)
		n.Dependencies[fieldName] = child
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

		tag := dependencyTag(fieldType)
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

// findStructDeps inspects exported fields for `kernel` tags, falling back to `core`,
// to determine dependency adapter ids.
// It returns a deps map keyed by struct field name.
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

		if sf.PkgPath != "" {
			continue
		}

		tag := dependencyTag(sf)
		if tag == "" {
			continue
		}

		parts := strings.Split(tag, ",")

		var adapterID string

		switch {
		case len(parts) >= 2:
			adapterID = strings.TrimSpace(parts[0])
		default:
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

	for k, v := range config {
		out[k] = v
	}
	for k, v := range inferred {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}
