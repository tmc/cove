package vmtree

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestPrint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	writeTreeVM(t, "base", vmconfig.Config{})
	writeTreeVM(t, "child-b", vmconfig.Config{
		ParentVM:       "base",
		ParentSnapshot: "seeded",
		ForkedAt:       time.Date(2026, time.April, 21, 8, 0, 0, 0, time.UTC),
	})
	writeTreeVM(t, "child-a", vmconfig.Config{
		ParentVM: "base",
		ForkedAt: time.Date(2026, time.April, 20, 8, 0, 0, 0, time.UTC),
	})
	writeTreeVM(t, "grandchild", vmconfig.Config{
		ParentVM:       "child-a",
		ParentSnapshot: "clean",
		ForkedAt:       time.Date(2026, time.April, 22, 8, 0, 0, 0, time.UTC),
	})
	writeTreeVM(t, "orphan", vmconfig.Config{
		ParentVM: "missing",
		ForkedAt: time.Date(2026, time.April, 23, 8, 0, 0, 0, time.UTC),
	})

	var buf bytes.Buffer
	if err := Print(&buf); err != nil {
		t.Fatalf("Print() error = %v", err)
	}
	want := `base
|-- child-a (forked 2026-04-20)
|   ` + "`" + `-- grandchild (from snapshot clean, forked 2026-04-22)
` + "`" + `-- child-b (from snapshot seeded, forked 2026-04-21)

orphan (forked 2026-04-23)
`
	if got := buf.String(); got != want {
		t.Fatalf("Print() =\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintVMTreeEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	if err := Print(&buf); err != nil {
		t.Fatalf("Print() error = %v", err)
	}
	if got, want := buf.String(), "No VMs found.\n"; got != want {
		t.Fatalf("Print() = %q, want %q", got, want)
	}
}

// TestPrintWithOptions_JSON asserts --json emits a structured
// forest where roots and orphans are top-level entries and children
// nest. Orphan nodes carry the orphan flag; roots do not.
func TestPrintWithOptions_JSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	writeTreeVM(t, "base", vmconfig.Config{})
	writeTreeVM(t, "child", vmconfig.Config{
		ParentVM: "base",
		ForkedAt: time.Date(2026, time.April, 21, 8, 0, 0, 0, time.UTC),
	})
	writeTreeVM(t, "lost", vmconfig.Config{
		ParentVM: "missing",
		ForkedAt: time.Date(2026, time.April, 22, 8, 0, 0, 0, time.UTC),
	})

	var buf bytes.Buffer
	if err := PrintWithOptions(&buf, Options{JSON: true}); err != nil {
		t.Fatalf("PrintWithOptions(JSON) error = %v", err)
	}

	var got []vmTreeJSONNode
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 top-level nodes (base + lost), got %d:\n%s", len(got), buf.String())
	}

	byName := map[string]vmTreeJSONNode{}
	for _, n := range got {
		byName[n.Name] = n
	}
	base, ok := byName["base"]
	if !ok {
		t.Fatalf("missing 'base' in JSON: %s", buf.String())
	}
	if base.Orphan {
		t.Errorf("base.Orphan = true, want false")
	}
	if len(base.Children) != 1 || base.Children[0].Name != "child" {
		t.Errorf("base.Children = %+v, want one child named 'child'", base.Children)
	}
	lost, ok := byName["lost"]
	if !ok {
		t.Fatalf("missing 'lost' in JSON: %s", buf.String())
	}
	if !lost.Orphan {
		t.Errorf("lost.Orphan = false, want true")
	}
	if lost.ParentVM != "missing" {
		t.Errorf("lost.ParentVM = %q, want %q", lost.ParentVM, "missing")
	}
}

// TestPrintWithOptions_OrphansASCII pins the --orphans flat
// listing: only orphans appear, formatted with the missing parent
// reference, and no tree branches are drawn.
func TestPrintWithOptions_OrphansASCII(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	writeTreeVM(t, "base", vmconfig.Config{})
	writeTreeVM(t, "child", vmconfig.Config{
		ParentVM: "base",
		ForkedAt: time.Date(2026, time.April, 21, 8, 0, 0, 0, time.UTC),
	})
	writeTreeVM(t, "lost", vmconfig.Config{
		ParentVM:       "missing",
		ParentSnapshot: "clean",
		ForkedAt:       time.Date(2026, time.April, 22, 8, 0, 0, 0, time.UTC),
	})

	var buf bytes.Buffer
	if err := PrintWithOptions(&buf, Options{Orphans: true}); err != nil {
		t.Fatalf("PrintWithOptions(Orphans) error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "lost (parent missing: missing@clean, forked 2026-04-22)") {
		t.Errorf("orphan output missing expected line; got:\n%s", got)
	}
	if strings.Contains(got, "base") {
		t.Errorf("orphan output included root 'base'; got:\n%s", got)
	}
	if strings.Contains(got, "child") {
		t.Errorf("orphan output included non-orphan child; got:\n%s", got)
	}
}

// TestPrintWithOptions_NoOrphansWithFlag pins the empty case
// for --orphans: when there are no orphan VMs, the output is the
// "No orphan VMs." sentinel rather than an empty string.
func TestPrintWithOptions_NoOrphansWithFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "solo", vmconfig.Config{})

	var buf bytes.Buffer
	if err := PrintWithOptions(&buf, Options{Orphans: true}); err != nil {
		t.Fatalf("PrintWithOptions(Orphans) error = %v", err)
	}
	if got, want := buf.String(), "No orphan VMs.\n"; got != want {
		t.Fatalf("PrintWithOptions(Orphans) = %q, want %q", got, want)
	}
}

func writeTreeVM(t *testing.T, name string, cfg vmconfig.Config) {
	t.Helper()

	dir := filepath.Join(vmconfig.BaseDir(), name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(%s/linux-disk.img) error = %v", name, err)
	}
	if err := vmconfig.Save(dir, &cfg); err != nil {
		t.Fatalf("vmconfig.Save(%s) error = %v", name, err)
	}
}

func reachableImage(ref string, names ...string) *ReachableImage {
	return &ReachableImage{
		Ref:    ref,
		Exists: func(string) bool { return true },
		Children: func(string) ([]string, error) {
			return append([]string(nil), names...), nil
		},
	}
}

func TestVMTree_ReachableFromImage_Forest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := "base:v1"
	writeTreeVM(t, "eval-001", vmconfig.Config{
		ParentImage: ref,
		ForkedAt:    time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC),
	})
	writeTreeVM(t, "eval-002", vmconfig.Config{
		ParentImage: ref,
		ForkedAt:    time.Date(2026, 5, 3, 12, 35, 14, 0, time.UTC),
	})
	writeTreeVM(t, "unrelated", vmconfig.Config{})

	var buf bytes.Buffer
	if err := PrintWithOptions(&buf, Options{ReachableFromImage: reachableImage(ref, "eval-001", "eval-002")}); err != nil {
		t.Fatalf("PrintWithOptions(ReachableFrom): %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "base:v1 (image)") {
		t.Errorf("missing synthetic image root; got:\n%s", got)
	}
	for _, want := range []string{"eval-001", "eval-002"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unrelated") {
		t.Errorf("output unexpectedly contains 'unrelated':\n%s", got)
	}
}

func TestVMTree_ReachableFromImage_JSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := "base:v1"
	for _, name := range []string{"eval-001", "eval-002", "eval-003-orphan"} {
		cfg := vmconfig.Config{
			ParentImage: ref,
			ForkedAt:    time.Date(2026, 5, 3, 12, 36, 0, 0, time.UTC),
		}
		if name == "eval-003-orphan" {
			cfg.ParentVM = "long-gone-parent"
		}
		writeTreeVM(t, name, cfg)
	}

	var buf bytes.Buffer
	if err := PrintWithOptions(&buf, Options{ReachableFromImage: reachableImage(ref, "eval-001", "eval-002", "eval-003-orphan"), JSON: true}); err != nil {
		t.Fatalf("PrintWithOptions(ReachableFrom, JSON): %v", err)
	}

	var got reachableImageJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}
	if got.Image != "base:v1" {
		t.Errorf("image = %q, want base:v1", got.Image)
	}
	if len(got.Children) != 3 {
		t.Fatalf("len(children) = %d, want 3:\n%s", len(got.Children), buf.String())
	}
	byName := map[string]reachableChildJSON{}
	for _, c := range got.Children {
		byName[c.Name] = c
	}
	orphan, ok := byName["eval-003-orphan"]
	if !ok {
		t.Fatalf("missing eval-003-orphan: %s", buf.String())
	}
	if !orphan.Orphan {
		t.Errorf("eval-003-orphan.Orphan = false, want true")
	}
	if normal := byName["eval-001"]; normal.Orphan {
		t.Errorf("eval-001.Orphan = true, want false")
	}
}

func TestVMTree_ReachableFromImage_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := &ReachableImage{
		Ref:    "ghost:v1",
		Exists: func(string) bool { return false },
	}
	err := PrintWithOptions(io.Discard, Options{ReachableFromImage: missing})
	if err == nil {
		t.Fatal("PrintWithOptions on missing image succeeded; want error")
	}
	if !strings.Contains(err.Error(), "ghost:v1") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q does not flag missing image", err)
	}
}

func TestVMTree_ReachableFromImage_OrphansFlagConflict(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := PrintWithOptions(io.Discard, Options{
		ReachableFromImage: reachableImage("base:v1"),
		Orphans:            true,
	})
	if err == nil {
		t.Fatal("PrintWithOptions accepted both flags; want conflict error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q does not flag the conflict", err)
	}
}
