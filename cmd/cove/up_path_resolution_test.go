package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestUpPathResolutionFreshNamedVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restoreVMGlobals(t)

	mainDir, err := vmconfig.EnsureDir("", "")
	if err != nil {
		t.Fatalf("main EnsureDir: %v", err)
	}
	vmDir = mainDir

	cfg, err := parseUpFlags(commandTestEnv(), []string{
		"-user", "smoketest",
		"-password", "smokepass123",
		"-vm", "smoketest-vm",
		"-ipsw", filepath.Join(home, "RestoreImage.ipsw"),
		"-headless",
	})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	wantDir := canonPath(t, filepath.Join(home, ".vz", "vms", "smoketest-vm"))
	if got := canonPath(t, cfg.vmDir); got != wantDir {
		t.Fatalf("cfg.vmDir = %q, want %q", got, wantDir)
	}
	if info, err := os.Stat(cfg.vmDir); err != nil {
		t.Fatalf("resolved vm dir does not exist: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("resolved vm dir is not a directory: mode=%v", info.Mode())
	}

	target := runtimeOptionsForUp(cfg).vmSelection()
	if target.Name != "smoketest-vm" {
		t.Fatalf("target.Name = %q, want smoketest-vm", target.Name)
	}
	if target.Directory != cfg.vmDir {
		t.Fatalf("target.Directory = %q, want %q", target.Directory, cfg.vmDir)
	}
	if got, want := target.diskPath(), filepath.Join(cfg.vmDir, "disk.img"); got != want {
		t.Fatalf("target.diskPath() = %q, want %q", got, want)
	}
}

func TestUpPathResolutionFreshDefaultVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restoreVMGlobals(t)

	mainDir, err := vmconfig.EnsureDir("", "")
	if err != nil {
		t.Fatalf("main EnsureDir: %v", err)
	}
	vmDir = mainDir

	cfg, err := parseUpFlags(commandTestEnv(), []string{
		"-user", "smoketest",
		"-password", "smokepass123",
		"-ipsw", filepath.Join(home, "RestoreImage.ipsw"),
		"-headless",
	})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	wantDir := canonPath(t, filepath.Join(home, ".vz", "vms", "default"))
	if got := canonPath(t, cfg.vmDir); got != wantDir {
		t.Fatalf("cfg.vmDir = %q, want %q", got, wantDir)
	}

	target := runtimeOptionsForUp(cfg).vmSelection()
	if target.Directory != cfg.vmDir {
		t.Fatalf("target.Directory = %q, want %q", target.Directory, cfg.vmDir)
	}
	if got, want := target.diskPath(), filepath.Join(cfg.vmDir, "disk.img"); got != want {
		t.Fatalf("target.diskPath() = %q, want %q", got, want)
	}
}

func TestUpPathResolutionWithLegacyVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restoreVMGlobals(t)

	legacyDir := filepath.Join(home, ".vz", "legacy-vm")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("MkdirAll(legacyDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "disk.img"), []byte("stub"), 0644); err != nil {
		t.Fatalf("WriteFile(disk.img): %v", err)
	}

	mainDir, err := vmconfig.EnsureDir("", "")
	if err != nil {
		t.Fatalf("main EnsureDir: %v", err)
	}
	vmDir = mainDir

	cfg, err := parseUpFlags(commandTestEnv(), []string{
		"-user", "smoketest",
		"-password", "smokepass123",
		"-vm", "legacy-vm",
		"-headless",
	})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if got, want := canonPath(t, cfg.vmDir), canonPath(t, legacyDir); got != want {
		t.Fatalf("cfg.vmDir = %q, want %q", got, want)
	}

	aliasPath := filepath.Join(home, ".vz", "vms", "legacy-vm")
	info, err := os.Lstat(aliasPath)
	if err != nil {
		t.Fatalf("alias not planted: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("alias %q is not a symlink (mode=%v)", aliasPath, info.Mode())
	}

	target := runtimeOptionsForUp(cfg).vmSelection()
	if target.Directory != cfg.vmDir {
		t.Fatalf("target.Directory = %q, want %q", target.Directory, cfg.vmDir)
	}
	if got, want := target.diskPath(), filepath.Join(cfg.vmDir, "disk.img"); got != want {
		t.Fatalf("target.diskPath() = %q, want %q", got, want)
	}
}

func TestRequireRootForMacOSUpProvisioningAllowsNativeAuth(t *testing.T) {
	restoreVMGlobals(t)
	oldEUID := upEffectiveUID
	t.Cleanup(func() { upEffectiveUID = oldEUID })

	cfg := upConfig{
		user:       "mlxqa",
		password:   "mlxqa123",
		vmName:     "mlxgo-fresh-nodev-20260505",
		cpuCount:   4,
		memoryGB:   8,
		gui:        false,
		noShutdown: true,
	}
	target := vmSelection{Name: cfg.vmName, Directory: t.TempDir()}
	upEffectiveUID = func() int { return 501 }
	t.Setenv("COVE_FORCE_MANUAL_ELEVATION", "")
	t.Setenv("CLAUDECODE", "")
	t.Setenv("IS_SANDBOX", "")

	if err := requireRootForMacOSUpProvisioning(cfg, target, false); err != nil {
		t.Fatalf("requireRootForMacOSUpProvisioning: %v", err)
	}

	upEffectiveUID = func() int { return 0 }
	if err := requireRootForMacOSUpProvisioning(cfg, target, false); err != nil {
		t.Fatalf("root requireRootForMacOSUpProvisioning: %v", err)
	}
}

func TestRequireRootForMacOSUpProvisioningRejectsManualElevationContext(t *testing.T) {
	restoreVMGlobals(t)
	oldEUID := upEffectiveUID
	t.Cleanup(func() { upEffectiveUID = oldEUID })

	cfg := upConfig{user: "mlxqa", password: "mlxqa123", vmName: "mlxgo-fresh-nodev-20260505"}
	target := vmSelection{Name: cfg.vmName, Directory: t.TempDir()}
	upEffectiveUID = func() int { return 501 }
	t.Setenv("COVE_FORCE_MANUAL_ELEVATION", "1")

	err := requireRootForMacOSUpProvisioning(cfg, target, false)
	if err == nil {
		t.Fatal("requireRootForMacOSUpProvisioning returned nil error")
	}
	if want := "auto-login provisioning needs the native macOS admin dialog"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if strings.Contains(err.Error(), "sudo") {
		t.Fatalf("error suggests sudo: %v", err)
	}
}

func restoreVMGlobals(t *testing.T) {
	t.Helper()
	saveVMName := vmName
	saveVMDir := vmDir
	saveIPS := ipswPath
	saveForce := forceInstall
	saveCPU := cpuCount
	saveMemory := memoryGB
	saveDiskSize := diskSizeGB
	saveVerbose := verbose
	saveGUI := guiMode
	saveAutomationBackend := automationBackend
	saveAutomationCaptureBackend := automationCaptureBackend
	saveAutomationInputBackend := automationInputBackend
	saveProvisionUser := provisionUser
	saveProvisionPassword := provisionPassword
	saveProvisionStrategy := provisionStrategy
	saveInstall := installVM
	saveLinux := linuxMode
	saveLinuxDesktop := linuxDesktop
	t.Cleanup(func() {
		vmName = saveVMName
		vmDir = saveVMDir
		ipswPath = saveIPS
		forceInstall = saveForce
		cpuCount = saveCPU
		memoryGB = saveMemory
		diskSizeGB = saveDiskSize
		verbose = saveVerbose
		guiMode = saveGUI
		automationBackend = saveAutomationBackend
		automationCaptureBackend = saveAutomationCaptureBackend
		automationInputBackend = saveAutomationInputBackend
		provisionUser = saveProvisionUser
		provisionPassword = saveProvisionPassword
		provisionStrategy = saveProvisionStrategy
		installVM = saveInstall
		linuxMode = saveLinux
		linuxDesktop = saveLinuxDesktop
	})
	vmName = ""
	vmDir = ""
	ipswPath = ""
	forceInstall = false
	cpuCount = 0
	memoryGB = 0
	diskSizeGB = 0
	verbose = false
	guiMode = true
	automationBackend = ""
	automationCaptureBackend = ""
	automationInputBackend = ""
	provisionUser = ""
	provisionPassword = ""
	provisionStrategy = ""
	installVM = false
	linuxMode = false
	linuxDesktop = false
}

func canonPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}
