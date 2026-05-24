package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/buildscratch"
)

func TestResolveControlTokenForSocketFromEnv(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "  envtok  ")
	if got := resolveControlTokenForSocket("/nonexistent/control.sock"); got != "envtok" {
		t.Errorf("env got %q, want %q", got, "envtok")
	}
}

func TestResolveControlTokenForSocketFromFile(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")
	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	tokFile := filepath.Join(dir, controlTokenFileName)
	if err := os.WriteFile(tokFile, []byte("filetok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := resolveControlTokenForSocket(sock); got != "filetok" {
		t.Errorf("file got %q, want %q", got, "filetok")
	}
}

func TestResolveControlTokenForSocketMissing(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")
	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	if got := resolveControlTokenForSocket(sock); got != "" {
		t.Errorf("missing got %q, want empty", got)
	}
}

func TestCompactBuildScratchFastNoop(t *testing.T) {
	sc := buildscratch.Scratch{Dir: t.TempDir()}
	if err := compactBuildScratch(context.Background(), sc, "fast"); err != nil {
		t.Errorf("fast: %v", err)
	}
}

func TestCompactBuildScratchEmptyDir(t *testing.T) {
	err := compactBuildScratch(context.Background(), buildscratch.Scratch{}, "fast")
	if err == nil || !strings.Contains(err.Error(), "scratch vm dir required") {
		t.Errorf("err = %v, want 'scratch vm dir required'", err)
	}
}

func TestCompactBuildScratchInvalidMode(t *testing.T) {
	sc := buildscratch.Scratch{Dir: t.TempDir()}
	err := compactBuildScratch(context.Background(), sc, "bogus")
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("err = %v, want 'invalid mode'", err)
	}
}

func TestCompactBuildScratchEmptyMode(t *testing.T) {
	sc := buildscratch.Scratch{Dir: t.TempDir()}
	err := compactBuildScratch(context.Background(), sc, "")
	if err == nil || !strings.Contains(err.Error(), "empty mode") {
		t.Errorf("err = %v, want 'empty mode'", err)
	}
}

func TestCompactBuildScratchCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sc := buildscratch.Scratch{Dir: t.TempDir()}
	if err := compactBuildScratch(ctx, sc, "fast"); err == nil {
		t.Error("cancelled ctx err = nil, want context.Canceled")
	}
}

func TestCompactBuildScratchNilContext(t *testing.T) {
	sc := buildscratch.Scratch{Dir: t.TempDir()}
	// nil context should be replaced with Background and succeed for fast mode.
	if err := compactBuildScratch(nil, sc, "fast"); err != nil {
		t.Errorf("nil ctx fast: %v", err)
	}
}
