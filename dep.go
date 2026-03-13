package kernel

import (
	"fmt"
	"reflect"
	"strings"

	"slices"

	"github.com/bartdeboer/words"
)

// applyDeps wires dependencies into an adapter using both map-style (Depender) and
// struct-field injection.
func applyDeps(adapter Adapter, parentWorkDir string, meta *MetaHeader) error {
	// infer from struct tags regardless of meta
	inferred, err := findStructDeps(adapter)
	if err != nil {
		return err
	}

	var cfg map[string]DepRef
	if meta != nil {
		cfg = meta.Dependencies
	}

	deps := mergeDeps(cfg, inferred)
	if deps == nil {
		return nil
	}

	if depender, ok := adapter.(Depender); ok {
		if err := resolveMapDeps(depender, parentWorkDir, deps); err != nil {
			return err
		}
	}
	if err := resolveStructDeps(adapter, parentWorkDir, deps); err != nil {
		return err
	}
	return nil
}

func resolveMapDeps(target Depender, parentWorkDir string, deps map[string]DepRef) error {
	for name, ref := range deps {
		var alias string
		switch {
		case ref.Name != "":
			alias = ref.Name
		case ref.Adapter != "":
			alias = ref.Adapter
		}

		// Check for existing that should be reused
		if alias != "" {
			depKey := strings.ToLower(ref.Adapter) + "__" + alias
			// adapters map is now inside the registry, but resolveMapDeps is only
			// called from NewAdapter after the registry has installed the adapter,
			// so reuse is handled there. This function only constructs new deps.
			_ = depKey
		}

		// Otherwise create a new instance
		var depArgs []string
		if ref.Name != "" {
			depArgs = append(depArgs, ref.Name)
		}
		depArgs = append(depArgs, ref.Args...)

		depAdapter, err := newAdapterWithContext(ref.Adapter, parentWorkDir, depArgs...)
		if err != nil {
			return fmt.Errorf("failed loading dependency %q: %w", name, err)
		}
		target.AddDependency(name, depAdapter)
	}
	return nil
}

// resolveStructDeps initialises and assigns dependencies to exported
// pointer fields on the parent whose names match deps' keys.
func resolveStructDeps(target any, parentWorkDir string, deps map[string]DepRef) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("resolveStructDeps: target must be a pointer, got %T", target)
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("resolveStructDeps: target must point to a struct, got %T", target)
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

		childArgs := slices.Clone(ref.Args)
		if ref.Name != "" {
			childArgs = append([]string{ref.Name}, childArgs...)
		}

		// Pass the parent context path
		dep, err := newAdapterWithContext(ref.Adapter, parentWorkDir, childArgs...)
		if err != nil {
			return fmt.Errorf("dependency %q: %w", fieldName, err)
		}

		depVal := reflect.ValueOf(dep)
		if !depVal.Type().AssignableTo(field.Type()) {
			return fmt.Errorf("dependency %q (%s) not assignable to field %s (%s)",
				fieldName, depVal.Type(), fieldName, field.Type())
		}

		Log().Debugf("assigned %s to %s %s\n",
			depVal.Type(), fieldName, field.Type())

		field.Set(depVal)
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

		// Only exported fields
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
			// Positional form: first token is the adapter key (even if it's "required").
			adapterID = strings.TrimSpace(parts[0])
		default:
			// Single token form: either a flag (required) or a key.
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

	// config first (wins)
	for k, v := range config {
		out[k] = v
	}
	// fill missing from inferred
	for k, v := range inferred {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}
