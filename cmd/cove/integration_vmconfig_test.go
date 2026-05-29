//go:build integration && darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testVMConfig(t *testing.T, vm *testVM) {
	t.Run("export-import", func(t *testing.T) {
		cloneName := integrationCloneName(t.Name())
		if err := CloneVM(CloneOptions{Source: vm.name, Target: cloneName, Linked: true}); err != nil {
			t.Fatalf("CloneVM() error = %v", err)
		}
		clone := clonedTestVM(t, cloneName, vm.linux)

		exportPath := filepath.Join(t.TempDir(), "framework-config.vzcfg")

		args := []string{"-vm", clone.name}
		if clone.linux {
			args = append(args, "-linux")
		}
		args = append(args, "vm", "config", "export", exportPath)
		out := runIntegrationBinaryCommandExpectSuccess(t, args...)
		if !strings.Contains(out, "Wrote ") {
			t.Fatalf("export output missing write confirmation:\n%s", out)
		}
		info, err := os.Stat(exportPath)
		if err != nil {
			t.Fatalf("stat exported config %q: %v", exportPath, err)
		}
		if info.Size() == 0 {
			t.Fatalf("exported config %q is empty", exportPath)
		}

		storedPath := filepath.Join(clone.dir, vmFrameworkConfigFileName)
		if err := os.Remove(storedPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove stored config %q: %v", storedPath, err)
		}

		args = []string{"-vm", clone.name}
		if clone.linux {
			args = append(args, "-linux")
		}
		args = append(args, "vm", "config", "import", exportPath)
		out = runIntegrationBinaryCommandExpectSuccess(t, args...)
		if !strings.Contains(out, "Stored raw snapshot") {
			t.Fatalf("import output missing stored snapshot confirmation:\n%s", out)
		}
		info, err = os.Stat(storedPath)
		if err != nil {
			t.Fatalf("stat stored config %q: %v", storedPath, err)
		}
		if info.Size() == 0 {
			t.Fatalf("stored config %q is empty", storedPath)
		}
	})
}
