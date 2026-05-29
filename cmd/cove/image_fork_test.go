package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/imagestore"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestRunImageForkFromWithConfigInvalidRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := runImageForkFromWithConfig(RunConfig{EphemeralForkParent: "::bad"}, "", "", nil)
	if err == nil {
		t.Fatal("runImageForkFromWithConfig(::bad) = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "cove run -fork-from") {
		t.Fatalf("err = %v, want 'cove run -fork-from' wrap", err)
	}
}

func TestRunImageForkFromWithConfigMissingImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := runImageForkFromWithConfig(RunConfig{EphemeralForkParent: "ghost-image:v1"}, "", "", nil)
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

func TestRunForkFromUntaggedMissingParentGivesImageAndVMHints(t *testing.T) {
	home := withTempHome(t)
	err := runVMWithConfig(RunConfig{
		VM:                  vmSelection{Name: "default", Directory: filepath.Join(vmconfig.BaseDir(), "default")},
		EphemeralForkParent: "ghost-image",
	})
	if err == nil {
		t.Fatal("runVMWithConfig missing untagged fork parent succeeded")
	}
	for _, want := range []string{
		`no VM named "ghost-image"`,
		"no local image ghost-image:latest",
		"cove image list",
		"cove image search ghost-image",
		"cove fork ghost-image <child>",
		"cove clone --linked ghost-image <child>",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(statErr) {
		t.Fatalf("default VM dir stat = %v, want not exist", statErr)
	}
}

func TestRunMissingImageForkFromDoesNotCreateDefaultOrRunDirs(t *testing.T) {
	home := withTempHome(t)
	err := runVMWithConfig(RunConfig{
		VM:                  vmSelection{Name: "default", Directory: filepath.Join(vmconfig.BaseDir(), "default")},
		EphemeralForkParent: "ghost-image:v1",
		Ephemeral:           true,
	})
	if err == nil {
		t.Fatal("runVMWithConfig missing image fork succeeded")
	}
	if !strings.Contains(err.Error(), "image ghost-image:v1 not found") {
		t.Fatalf("err = %v, want missing image", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(statErr) {
		t.Fatalf("default VM dir stat = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".vz", "runs")); !os.IsNotExist(statErr) {
		t.Fatalf("runs dir stat = %v, want not exist", statErr)
	}
}

func TestRunBadImageForkFromDoesNotCreateRunBundle(t *testing.T) {
	home := withTempHome(t)
	err := runVMWithConfig(RunConfig{
		VM:                  vmSelection{Name: "default", Directory: filepath.Join(vmconfig.BaseDir(), "default")},
		EphemeralForkParent: "::bad",
		Ephemeral:           true,
	})
	if err == nil {
		t.Fatal("runVMWithConfig bad image fork succeeded")
	}
	if !strings.Contains(err.Error(), "cove run -fork-from <image>") {
		t.Fatalf("err = %v, want image fork-from parse error", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(statErr) {
		t.Fatalf("default VM dir stat = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".vz", "runs")); !os.IsNotExist(statErr) {
		t.Fatalf("runs dir stat = %v, want not exist", statErr)
	}
}

func TestRunExistingImageForkFromStillCreatesRunBundle(t *testing.T) {
	home := withTempHome(t)
	stageMacOSVMForImage(t, "src-existing-image")
	ref, err := ParseImageRef("existing-image:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src-existing-image", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	hooks, _ := stubAcquireRunLockHook(t)
	hooks.RunMacOSVM = runHook(func() error { return nil })

	err = runVMWithConfig(RunConfig{
		VM:                  vmSelection{Name: "default", Directory: filepath.Join(vmconfig.BaseDir(), "default")},
		Hooks:               hooks,
		EphemeralForkParent: ref.String(),
		Ephemeral:           true,
	})
	if err != nil {
		t.Fatalf("runVMWithConfig existing image fork: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".vz", "runs"))
	if err != nil {
		t.Fatalf("read runs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("run bundle entries = %d, want 1", len(entries))
	}
}

func TestIsImageForkFromRefTreatsMissingTaggedRefAsImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if !isImageForkFromRef("name:missing") {
		t.Fatal("isImageForkFromRef(name:missing) = false, want true")
	}
}

func TestMaybeQuietImageForkSerial(t *testing.T) {
	oldHeadless := headlessMode
	oldSerial := serialOutput
	t.Cleanup(func() {
		headlessMode = oldHeadless
		serialOutput = oldSerial
	})
	headlessMode = true
	serialOutput = "stdout"

	stderr, restore := captureStderr(t)
	cleanup := maybeQuietImageForkSerial(RunConfig{Linux: true})
	restore()
	if serialOutput != "none" {
		t.Fatalf("serialOutput = %q, want none", serialOutput)
	}
	if !strings.Contains(stderr.String(), "suppressing Linux serial console") {
		t.Fatalf("stderr = %q, want suppression note", stderr.String())
	}
	cleanup()
	if serialOutput != "stdout" {
		t.Fatalf("restored serialOutput = %q, want stdout", serialOutput)
	}
}

func TestRunConfigForImageManifestInfersLinux(t *testing.T) {
	cfg := runConfigForImageManifest(RunConfig{}, &imagestore.Manifest{OSType: "Linux"})
	if !cfg.Linux {
		t.Fatal("Linux = false, want true")
	}
	if cfg.Windows {
		t.Fatal("Windows = true, want false")
	}
}

func TestRunConfigForImageManifestInfersWindows(t *testing.T) {
	cfg := runConfigForImageManifest(RunConfig{Linux: true}, &imagestore.Manifest{OSType: "Windows"})
	if !cfg.Windows {
		t.Fatal("Windows = false, want true")
	}
	if cfg.Linux {
		t.Fatal("Linux = true, want false")
	}
}
