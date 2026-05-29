package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyProvisioningAlreadyAppliedWithoutStaging(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := vmSelection{Directory: t.TempDir(), Name: "already-done"}
	markInjectSucceededForVM(target)

	out, err := captureStdoutResult(t, func() error {
		return applyProvisioningFilesForVM(target)
	})
	if err != nil {
		t.Fatalf("applyProvisioningFilesForVM: %v", err)
	}
	for _, want := range []string{
		`Provisioning already applied to "already-done".`,
		"cove -vm already-done provision -user <username> -password <password> -force",
		"cove -vm already-done verify",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q\n%s", want, out)
		}
	}
}

func TestApplyProvisioningMissingStagingWithoutSuccessMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := vmSelection{Directory: t.TempDir(), Name: "not-staged"}

	out, err := captureStdoutResult(t, func() error {
		return applyProvisioningFilesForVM(target)
	})
	if err == nil {
		t.Fatal("applyProvisioningFilesForVM succeeded, want missing staging error")
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	for _, want := range []string{
		"no staged provisioning files found",
		"cove -vm not-staged provision -user <username> -password <password>",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "cove inject") {
		t.Fatalf("error suggests old inject command:\n%v", err)
	}
}

func TestApplyProvisioningForceIgnoresSuccessMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := vmSelection{Directory: t.TempDir(), Name: "force-apply"}
	if err := os.WriteFile(target.diskPath(), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	stagingDir := provisionStagingDirForVM(target)
	if err := os.MkdirAll(filepath.Join(stagingDir, "private", "var", "db"), 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := writeManifest(stagingDir, &ProvisionManifest{Version: 1}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	markInjectSucceededForVM(target)

	oldPreWarm := preWarmAuthorizationHook
	oldAttach := attachAndMountDataVolumeHook
	oldDetach := detachDiskHook
	oldApply := applyStagedFilesHook
	t.Cleanup(func() {
		preWarmAuthorizationHook = oldPreWarm
		attachAndMountDataVolumeHook = oldAttach
		detachDiskHook = oldDetach
		applyStagedFilesHook = oldApply
	})

	var applied bool
	preWarmAuthorizationHook = func() error { return nil }
	attachAndMountDataVolumeHook = func(string) (string, string, string, error) {
		return t.TempDir(), "/dev/disk-test", "disk-test-data", nil
	}
	detachDiskHook = func(string) {}
	applyStagedFilesHook = func(vmSelection, string, string, string, *ProvisionManifest) error {
		applied = true
		return nil
	}

	out, err := captureStdoutResult(t, func() error {
		return applyProvisioningFilesForVMForce(target, true)
	})
	if err != nil {
		t.Fatalf("applyProvisioningFilesForVMForce: %v", err)
	}
	if !applied {
		t.Fatal("applyStagedFilesHook was not called")
	}
	if strings.Contains(out, "already applied") {
		t.Fatalf("stdout reported already applied with -force:\n%s", out)
	}
}

func TestProvisionCommandAlreadyAppliedDoesNotRequireUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMDir, oldVMName := vmDir, vmName
	t.Cleanup(func() {
		vmDir, vmName = oldVMDir, oldVMName
	})
	vmDir = t.TempDir()
	vmName = "already-command"
	markInjectSucceededForVM(currentVMSelection())

	out, err := captureStdoutResult(t, func() error {
		return handleProvision(nil)
	})
	if err != nil {
		t.Fatalf("handleProvision: %v", err)
	}
	if !strings.Contains(out, `Provisioning already applied to "already-command".`) {
		t.Fatalf("stdout missing already applied message:\n%s", out)
	}
}
