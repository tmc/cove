package main

import (
	"bytes"
	"os"
	"path/filepath"
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
