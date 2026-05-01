package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestPrintVMTree(t *testing.T) {
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
	if err := PrintVMTree(&buf); err != nil {
		t.Fatalf("PrintVMTree() error = %v", err)
	}
	want := `base
|-- child-a (forked 2026-04-20)
|   ` + "`" + `-- grandchild (from snapshot clean, forked 2026-04-22)
` + "`" + `-- child-b (from snapshot seeded, forked 2026-04-21)

orphan (forked 2026-04-23)
`
	if got := buf.String(); got != want {
		t.Fatalf("PrintVMTree() =\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintVMTreeEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	if err := PrintVMTree(&buf); err != nil {
		t.Fatalf("PrintVMTree() error = %v", err)
	}
	if got, want := buf.String(), "No VMs found.\n"; got != want {
		t.Fatalf("PrintVMTree() = %q, want %q", got, want)
	}
}

// TestPrintVMTreeWithOptions_JSON asserts --json emits a structured
// forest where roots and orphans are top-level entries and children
// nest. Orphan nodes carry the orphan flag; roots do not.
func TestPrintVMTreeWithOptions_JSON(t *testing.T) {
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
	if err := PrintVMTreeWithOptions(&buf, VMTreeOptions{JSON: true}); err != nil {
		t.Fatalf("PrintVMTreeWithOptions(JSON) error = %v", err)
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

// TestPrintVMTreeWithOptions_OrphansASCII pins the --orphans flat
// listing: only orphans appear, formatted with the missing parent
// reference, and no tree branches are drawn.
func TestPrintVMTreeWithOptions_OrphansASCII(t *testing.T) {
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
	if err := PrintVMTreeWithOptions(&buf, VMTreeOptions{Orphans: true}); err != nil {
		t.Fatalf("PrintVMTreeWithOptions(Orphans) error = %v", err)
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

// TestPrintVMTreeWithOptions_NoOrphansWithFlag pins the empty case
// for --orphans: when there are no orphan VMs, the output is the
// "No orphan VMs." sentinel rather than an empty string.
func TestPrintVMTreeWithOptions_NoOrphansWithFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "solo", vmconfig.Config{})

	var buf bytes.Buffer
	if err := PrintVMTreeWithOptions(&buf, VMTreeOptions{Orphans: true}); err != nil {
		t.Fatalf("PrintVMTreeWithOptions(Orphans) error = %v", err)
	}
	if got, want := buf.String(), "No orphan VMs.\n"; got != want {
		t.Fatalf("PrintVMTreeWithOptions(Orphans) = %q, want %q", got, want)
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
