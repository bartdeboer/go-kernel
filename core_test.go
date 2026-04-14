package kernel_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kernel "github.com/bartdeboer/go-kernel"
)

type Lister interface {
	List(ctx context.Context) ([]string, error)
}

// ListerAdp implements the local Lister interface and is configurable via lister-adp.json.
type ListerAdp struct {
	Note    string `json:"note"`
	WorkDir string
}

func (l *ListerAdp) List(ctx context.Context) ([]string, error) {
	return []string{"one", "two"}, nil
}

// ConfigPtr makes ListerAdp implement kernel.Configurable so lister-adp.json is used.
func (l *ListerAdp) ConfigPtr() any {
	return l
}

// SetWorkDir makes ListerAdp implement kernel.WorkDirSettable.
func (l *ListerAdp) SetWorkDir(path string) {
	l.WorkDir = path
}

// ChildAdp has NO config file => should receive propagated parent context.
type ChildAdp struct {
	WorkDir string
}

func (c *ChildAdp) List(ctx context.Context) ([]string, error) { return []string{"child"}, nil }
func (c *ChildAdp) SetWorkDir(path string)                     { c.WorkDir = path }

// Adp is the main adapter under test.
// It uses a single Spec struct for both adapter-level and item-level config.
// Item config should override adapter config fields.
type Adp struct {
	Spec struct {
		Foo   string `json:"foo"`
		Label string `json:"label"`
	}

	WorkDir string

	// Injected from dependencies in adp.json:
	// "dependencies": { "ListerProvider": { "adapter": "lister-adp" } }
	ListerProvider Lister `core:"required"`
	ChildProvider  Lister `core:"required"` // has NO config/context; should inherit parent
}

// ConfigPtr makes Adp implement kernel.Configurable.
func (a *Adp) ConfigPtr() any {
	return &a.Spec
}

// ItemConfigPtr makes Adp implement kernel.ItemConfigurable.
// We deliberately return the same Spec pointer so item config overlays adapter config.
func (a *Adp) ItemConfigPtr(name string) any {
	return &a.Spec
}

// SetWorkDir makes Adp implement kernel.WorkDirSettable.
func (a *Adp) SetWorkDir(path string) {
	a.WorkDir = path
}

func TestAdapter_ConfigOverride_DependencyInjection_AndContext(t *testing.T) {
	// Use configs from ./testdata.
	if _, err := kernel.SetDefaultSearchPath("testdata"); err != nil {
		t.Fatalf("SearchMap: %v", err)
	}

	// Register adapters.
	kernel.Register("lister-adp", func() kernel.Adapter { return &ListerAdp{} })
	kernel.Register("child-adp", func() kernel.Adapter { return &ChildAdp{} })
	kernel.Register("adp", func() kernel.Adapter { return &Adp{} })

	// Create an instance of "adp" using the item config "items/inst1".
	// Uses:
	//   - testdata/adp.json         (adapter-level config + context)
	//   - testdata/items/inst1.json (item-level config + context)
	//   - testdata/lister-adp.json  (dependency config + context)
	adp, err := kernel.NewAdapterAs[*Adp]("adp", "items/inst1")
	if err != nil {
		t.Fatalf("NewAdapterAs(adp): %v", err)
	}

	// --- Config overlay behavior ---
	// From adp.json:
	//   "foo": "global-foo"
	// From items/inst1.json:
	//   "foo": "item-foo"
	//   "label": "instance-1"
	//
	// Because both unmarshal into the same struct, item config should override.
	if got, want := adp.Spec.Foo, "item-foo"; got != want {
		t.Fatalf("Spec.Foo = %q, want %q (item config should override adapter config)", got, want)
	}
	if got, want := adp.Spec.Label, "instance-1"; got != want {
		t.Fatalf("Spec.Label = %q, want %q", got, want)
	}

	// --- Dependency injection into struct field ---
	if adp.ListerProvider == nil {
		t.Fatalf("ListerProvider is nil; dependency injection failed")
	}

	// Ensure the injected type is our ListerAdp.
	lister, ok := adp.ListerProvider.(*ListerAdp)
	if !ok {
		t.Fatalf("ListerProvider has type %T, want *ListerAdp", adp.ListerProvider)
	}
	child, ok := adp.ChildProvider.(*ChildAdp)
	if !ok {
		t.Fatalf("ChildProvider has type %T, want *ChildAdp", adp.ChildProvider)
	}

	// And that its config was loaded from lister-adp.json.
	if got, want := lister.Note, "dummy provider config"; got != want {
		t.Fatalf("ListerAdp.Note = %q, want %q", got, want)
	}

	// --- Behavior of the injected lister ---
	gotList, err := adp.ListerProvider.List(context.Background())
	if err != nil {
		t.Fatalf("ListerProvider.List error: %v", err)
	}
	wantList := []string{"one", "two"}
	if !reflect.DeepEqual(gotList, wantList) {
		t.Fatalf("ListerProvider.List() = %#v, want %#v", gotList, wantList)
	}

	// --- Context handling: exact absolute paths ---

	// Adp: item context should win.
	// inst1.json is at testdata/items/inst1.json with:
	//   "context": "ctx-inst1"
	// The code resolves that relative to the file's directory:
	//   Abs("testdata/items/ctx-inst1")
	expectedAdpCtx, err := filepath.Abs(filepath.Join("ctx-inst1"))
	if err != nil {
		t.Fatalf("filepath.Abs for Adp: %v", err)
	}
	if adp.WorkDir != expectedAdpCtx {
		t.Fatalf("Adp.WorkDir = %q, want %q", adp.WorkDir, expectedAdpCtx)
	}

	// ListerAdp: only adapter-level context.
	// lister-adp.json is at testdata/lister-adp.json with:
	//   "context": "ctx-lister"
	// The code resolves that relative to the file's directory:
	//   Abs("testdata/ctx-lister")
	expectedListerCtx, err := filepath.Abs(filepath.Join("testdata", "ctx-lister"))
	if err != nil {
		t.Fatalf("filepath.Abs for ListerAdp: %v", err)
	}
	if lister.WorkDir != expectedListerCtx {
		t.Fatalf("ListerAdp.WorkDir = %q, want %q", lister.WorkDir, expectedListerCtx)
	}

	// ChildAdp has no config => should inherit the parent's resolved context.
	if child.WorkDir != expectedAdpCtx {
		t.Fatalf("ChildAdp.WorkDir = %q, want %q (inherited from parent)", child.WorkDir, expectedAdpCtx)
	}
}

func TestAdapter_ContextAffectsDependencyReuse(t *testing.T) {
	if _, err := kernel.SetDefaultSearchPath("testdata"); err != nil {
		t.Fatalf("SearchMap: %v", err)
	}

	// Re-registering in a second test is fine in this package; keys are per-process.
	kernel.Register("lister-adp", func() kernel.Adapter { return &ListerAdp{} })
	kernel.Register("child-adp", func() kernel.Adapter { return &ChildAdp{} })
	kernel.Register("adp", func() kernel.Adapter { return &Adp{} })

	a1, err := kernel.NewAdapterAs[*Adp]("adp", "items/inst1")
	if err != nil {
		t.Fatalf("NewAdapterAs(inst1): %v", err)
	}
	a2, err := kernel.NewAdapterAs[*Adp]("adp", "items/inst2")
	if err != nil {
		t.Fatalf("NewAdapterAs(inst2): %v", err)
	}

	l1 := a1.ListerProvider.(*ListerAdp)
	l2 := a2.ListerProvider.(*ListerAdp)
	if l1 != l2 {
		t.Fatalf("expected lister-adp to be reused (same ctx-lister), but got different instances: %p != %p", l1, l2)
	}

	c1 := a1.ChildProvider.(*ChildAdp)
	c2 := a2.ChildProvider.(*ChildAdp)
	if c1 == c2 {
		t.Fatalf("expected child-adp NOT to be reused (different parent contexts), but got same instance: %p", c1)
	}
}

type HydratingAdp struct {
	Calls *atomic.Int32
	Fail  *atomic.Bool
	Delay time.Duration
}

func (a *HydratingAdp) Hydrate(ctx context.Context) error {
	a.Calls.Add(1)
	if a.Delay > 0 {
		time.Sleep(a.Delay)
	}
	if a.Fail != nil && a.Fail.Load() {
		return fmt.Errorf("hydrate failed")
	}
	return nil
}

func uniqueAdapterID(t *testing.T, suffix string) string {
	base := strings.ToLower(t.Name() + "-" + suffix)
	base = strings.ReplaceAll(base, "/", "-")
	base = strings.ReplaceAll(base, " ", "-")
	return base
}

func TestConstruct_DoesNotCacheFailedNode(t *testing.T) {
	if _, err := kernel.SetDefaultSearchPath(t.TempDir()); err != nil {
		t.Fatalf("SearchMap: %v", err)
	}

	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)

	adapterID := uniqueAdapterID(t, "failing")
	kernel.Register(adapterID, func() kernel.Adapter {
		return &HydratingAdp{
			Calls: &calls,
			Fail:  &fail,
		}
	})

	if _, err := kernel.Construct(adapterID); err == nil {
		t.Fatalf("expected first construct to fail")
	}

	fail.Store(false)

	node, err := kernel.Construct(adapterID)
	if err != nil {
		t.Fatalf("expected second construct to succeed, got %v", err)
	}
	if node == nil || node.Instance == nil {
		t.Fatalf("expected constructed node")
	}
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("hydrate calls = %d, want %d", got, want)
	}
}

func TestConstruct_ConcurrentReuseBuildsOnce(t *testing.T) {
	if _, err := kernel.SetDefaultSearchPath(t.TempDir()); err != nil {
		t.Fatalf("SearchMap: %v", err)
	}

	var calls atomic.Int32

	adapterID := uniqueAdapterID(t, "concurrent")
	kernel.Register(adapterID, func() kernel.Adapter {
		return &HydratingAdp{
			Calls: &calls,
			Delay: 25 * time.Millisecond,
		}
	})

	const workers = 8
	results := make(chan *kernel.Node, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node, err := kernel.Construct(adapterID)
			if err != nil {
				errs <- err
				return
			}
			results <- node
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("construct failed: %v", err)
		}
	}

	var first *kernel.Node
	for node := range results {
		if first == nil {
			first = node
			continue
		}
		if node != first {
			t.Fatalf("expected concurrent construct to reuse the same node instance")
		}
	}

	if got, want := calls.Load(), int32(1); got != want {
		t.Fatalf("hydrate calls = %d, want %d", got, want)
	}
}

func TestNodeString_IncludesConfigSourcePaths(t *testing.T) {
	if _, err := kernel.SetDefaultSearchPath("testdata"); err != nil {
		t.Fatalf("SearchMap: %v", err)
	}

	kernel.Register("lister-adp", func() kernel.Adapter { return &ListerAdp{} })
	kernel.Register("child-adp", func() kernel.Adapter { return &ChildAdp{} })
	kernel.Register("adp", func() kernel.Adapter { return &Adp{} })

	node, err := kernel.Construct("adp", "items/inst1")
	if err != nil {
		t.Fatalf("Construct(adp, items/inst1): %v", err)
	}

	graph := node.String()

	if !strings.Contains(graph, "name=inst1") {
		t.Fatalf("graph missing item name:\n%s", graph)
	}
	if !strings.Contains(graph, "cfg=items/inst1.json") {
		t.Fatalf("graph missing item config source:\n%s", graph)
	}
	if !strings.Contains(graph, "cfg=adp.json") {
		t.Fatalf("graph missing adapter config source:\n%s", graph)
	}
}
