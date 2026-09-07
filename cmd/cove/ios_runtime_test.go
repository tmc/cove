package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/iosbundle"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
)

func TestMacOSRunnerRejectsIOSBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 8, MemoryGB: 8, IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = runMacOSVMWithConfig(vmrun.RunConfig{OS: vmrun.GuestMacOS}, vmrun.HostConfig{VMDir: dir}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "ios bundle requires") {
		t.Fatalf("run = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		// vmconfig.Save leaves its lock file behind; it is not guest state.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) != 1 || names[0] != "config.json" {
		t.Fatalf("runner created guest state: %v", names)
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("runner rewrote ios configuration")
	}
}
