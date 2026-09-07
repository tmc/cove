//go:build darwin || linux

package firmware

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBuildInfo(t *testing.T) {
	name := filepath.Join(t.TempDir(), "info.swift")
	want := []byte("generated source\n")
	for range 2 {
		if err := writeBuildInfo(name, want); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeBuildInfo(name, []byte("different\n")); err == nil {
		t.Fatal("overwrote existing build info")
	}
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q", got)
	}
}

func TestBuildPatcherIntegration(t *testing.T) {
	dir := os.Getenv("COVE_TEST_PATCHER_DIR")
	if dir == "" {
		t.Skip("set COVE_TEST_PATCHER_DIR to build the pinned toolchain")
	}
	report, err := BuildPatcher(context.Background(), dir, os.Getenv("COVE_TEST_DEVELOPER_DIR"), os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if report.SourceCommit != SourceCommit || report.SwiftVersion == "" || report.SDKPath == "" || report.SDKVersion == "" || report.DeveloperDir == "" {
		t.Fatal(report)
	}
	digest, err := fileDigest(context.Background(), report.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if digest != report.SHA256 {
		t.Fatal("wrong binary digest")
	}
	if _, err := os.Stat(filepath.Join(dir, "patcher.json")); err != nil {
		t.Fatal(err)
	}
}

func ExampleBuildPatcher() {
	_, err := BuildPatcher(context.Background(), "", "", nil)
	fmt.Println(err)
	// Output: toolchain directory is required
}
