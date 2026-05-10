package main

import (
	"strings"
	"testing"
)

func TestRunImageForkFromWithConfigInvalidRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := runImageForkFromWithConfig(RunConfig{EphemeralForkParent: "::bad"}, "", "")
	if err == nil {
		t.Fatal("runImageForkFromWithConfig(::bad) = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "cove run -fork-from") {
		t.Fatalf("err = %v, want 'cove run -fork-from' wrap", err)
	}
}

func TestRunImageForkFromWithConfigMissingImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := runImageForkFromWithConfig(RunConfig{EphemeralForkParent: "ghost-image:v1"}, "", "")
	if err == nil {
		t.Fatal("runImageForkFromWithConfig(missing image) = nil, want not-found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want 'not found'", err)
	}
}

func TestRunConfigForImageManifestInfersLinux(t *testing.T) {
	cfg := runConfigForImageManifest(RunConfig{}, &ImageManifest{OSType: "Linux"})
	if !cfg.Linux {
		t.Fatal("Linux = false, want true")
	}
	if cfg.Windows {
		t.Fatal("Windows = true, want false")
	}
}

func TestRunConfigForImageManifestInfersWindows(t *testing.T) {
	cfg := runConfigForImageManifest(RunConfig{Linux: true}, &ImageManifest{OSType: "Windows"})
	if !cfg.Windows {
		t.Fatal("Windows = false, want true")
	}
	if cfg.Linux {
		t.Fatal("Linux = true, want false")
	}
}
