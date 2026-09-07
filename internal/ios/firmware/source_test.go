//go:build darwin || linux

package firmware

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSubmodules(t *testing.T) {
	hash := "1234567890123456789012345678901234567890"
	for _, tt := range []struct{ prefix, status string }{{" ", "clean"}, {"-", "uninitialized"}, {"+", "commit mismatch"}, {"U", "conflict"}} {
		modules, err := parseSubmodules(tt.prefix + hash + " vendor/library (heads/main)\n")
		if err != nil || len(modules) != 1 || modules[0].Status != tt.status || modules[0].Path != "vendor/library" {
			t.Fatalf("modules=%v err=%v", modules, err)
		}
	}
	for _, input := range []string{"x", "?" + hash + " vendor/library", " " + hash + " ../escape", " " + hash + " /absolute", " short vendor/library"} {
		if _, err := parseSubmodules(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestPrepareSourceFailureDoesNotReplaceExistingDirectory(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "keep")
	if err := os.WriteFile(marker, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareSource(context.Background(), existing, "missing"); err == nil {
		t.Fatal("accepted incomplete existing directory")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "new")
	if _, err := PrepareSource(context.Background(), target, filepath.Join(root, "missing-repository")); err == nil {
		t.Fatal("accepted missing repository")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("incomplete destination remains: %v", err)
	}
}

func TestCanceledSourcePreparationDoesNotMutate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	destination := filepath.Join(t.TempDir(), "source")
	if _, err := PrepareSource(ctx, destination, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("created directory: %v", err)
	}
}

func TestInspectSourceUnavailable(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, path, want string }{
		{"missing", filepath.Join(root, "missing"), "toolchain source directory is missing"},
		{"file", file, "toolchain source path is not a directory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report, err := InspectSource(context.Background(), tt.path)
			if err != nil || len(report.Problems) != 1 || report.Problems[0] != tt.want {
				t.Fatalf("report=%+v err=%v", report, err)
			}
		})
	}
}

func ExamplePrepareSource() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PrepareSource(ctx, "toolchain", "")
	fmt.Println(err)
	// Output: context canceled
}

func ExampleInspectSource() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := InspectSource(ctx, ".")
	fmt.Println(errors.Is(err, context.Canceled))
	// Output: true
}
