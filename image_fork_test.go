package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
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
	for _, want := range []string{
		"image ghost-image:v1 not found",
		"cove image list",
		"cove image search ghost-image",
		"cove image verify ghost-image:v1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
	}
}

func TestRunMissingImageForkFromDoesNotCreateSelectedVMDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	name := "missing-image-child"
	err := runVMWithConfig(RunConfig{
		VM:                  vmSelection{Name: name, Directory: filepath.Join(vmconfig.BaseDir(), name)},
		EphemeralForkParent: "ghost-image:v1",
		EphemeralForkName:   name,
	})
	if err == nil {
		t.Fatal("runVMWithConfig missing image fork succeeded")
	}
	if !strings.Contains(err.Error(), "image ghost-image:v1 not found") {
		t.Fatalf("err = %v, want missing image", err)
	}
	if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), name)); !os.IsNotExist(statErr) {
		t.Fatalf("selected VM dir stat = %v, want not exist", statErr)
	}
}

func TestIsImageForkFromRefTreatsMissingTaggedRefAsImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if !isImageForkFromRef("name:missing") {
		t.Fatal("isImageForkFromRef(name:missing) = false, want true")
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
