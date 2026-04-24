//go:build integration && darwin && arm64

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestLinuxProvisionArtifacts(t *testing.T) {
	name := strings.TrimSpace(*flagIntegrationLinuxVM)
	if name == "" {
		t.Skip("set -integration.linux-vm or VZ_TEST_LINUX_VM to a Linux VM name")
	}
	ensureIntegrationBaseVM(t, name, true)

	vmDir := resolvePath(vmconfig.Path(name))
	diskPath := filepath.Join(vmDir, "linux-disk.img")
	devices, err := attachLinuxDiskReadOnly(diskPath)
	if err != nil {
		t.Fatalf("attachLinuxDiskReadOnly(%q): %v", diskPath, err)
	}
	defer func() {
		if err := detachLinuxDisk(diskPath, devices); err != nil {
			t.Logf("detachLinuxDisk(%q): %v", diskPath, err)
		}
	}()
	if len(devices) < 3 {
		t.Fatalf("attached devices = %v, want at least disk + EFI + root", devices)
	}

	t.Run("efi-bootloader", func(t *testing.T) {
		mountPoint, err := mountLinuxEFIPartitionReadOnly(devices[1])
		if err != nil {
			t.Fatalf("mountLinuxEFIPartitionReadOnly(%q): %v", devices[1], err)
		}
		defer unmountLinuxEFIPartition(mountPoint)

		bootloaderPath := filepath.Join(mountPoint, "EFI", "BOOT", "BOOTAA64.EFI")
		if info, err := os.Stat(bootloaderPath); err != nil {
			t.Fatalf("stat %q: %v", bootloaderPath, err)
		} else if info.Size() == 0 {
			t.Fatalf("bootloader %q is empty", bootloaderPath)
		}

		grubCfgPath := filepath.Join(mountPoint, "EFI", "BOOT", "grub.cfg")
		grubCfg, err := os.ReadFile(grubCfgPath)
		if err != nil {
			t.Fatalf("read %q: %v", grubCfgPath, err)
		}
		if !bytes.Contains(grubCfg, []byte("configfile $prefix/grub.cfg")) {
			t.Fatalf("grub.cfg missing configfile handoff:\n%s", grubCfg)
		}

		artifactDir := filepath.Join(mountPoint, filepath.FromSlash(linuxEFIBootArtifactsDir))
		for _, name := range []string{"vmlinuz", "initrd", linuxRootUUIDFileName} {
			path := filepath.Join(artifactDir, name)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %q: %v", path, err)
			}
			if info.Size() == 0 {
				t.Fatalf("staged boot artifact %q is empty", path)
			}
		}
	})

	t.Run("rootfs-agent", func(t *testing.T) {
		rootDev := devices[2]

		agentBinary := copyExt4FileTB(t, rootDev, "/usr/local/bin/vz-agent")
		if len(agentBinary) == 0 {
			t.Fatal("vz-agent binary is empty")
		}

		service := string(copyExt4FileTB(t, rootDev, "/etc/systemd/system/vz-agent.service"))
		for _, want := range []string{
			"ExecStart=/bin/sh -c \"exec /usr/local/bin/vz-agent -mode daemon 2>&1 | tee -a /var/log/vz-agent.log\"",
			"modprobe vsock",
			"StandardOutput=journal+console",
			"StandardError=journal+console",
		} {
			if !strings.Contains(service, want) {
				t.Fatalf("vz-agent.service missing %q:\n%s", want, service)
			}
		}

		grubDefaults := string(copyExt4FileTB(t, rootDev, "/etc/default/grub"))
		if !strings.Contains(grubDefaults, `GRUB_CMDLINE_LINUX="console=tty0 console=hvc0"`) {
			t.Fatalf("/etc/default/grub missing serial console cmdline:\n%s", grubDefaults)
		}
	})

	t.Run("host-boot-artifacts", func(t *testing.T) {
		for _, name := range []string{"vmlinuz", "initrd", linuxRootUUIDFileName} {
			path := filepath.Join(vmDir, name)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %q: %v", path, err)
			}
			if info.Size() == 0 {
				t.Fatalf("host boot artifact %q is empty", path)
			}
		}
	})
}

func TestLinuxVMConfigIntegration(t *testing.T) {
	name := strings.TrimSpace(*flagIntegrationLinuxVM)
	if name == "" {
		t.Skip("set -integration.linux-vm or VZ_TEST_LINUX_VM to a Linux VM name")
	}
	ensureIntegrationBaseVM(t, name, true)
	vm := &testVM{
		name:  name,
		dir:   resolvePath(vmconfig.Path(name)),
		linux: true,
	}
	testVMConfig(t, vm)
}

func copyExt4FileTB(t testing.TB, device, guestPath string) []byte {
	t.Helper()

	e2cp, err := exec.LookPath("e2cp")
	if err != nil {
		t.Skip("e2cp not available")
	}

	tmpDir := t.TempDir()
	hostPath := filepath.Join(tmpDir, filepath.Base(guestPath))
	cmd := exec.Command(e2cp, device+":"+guestPath, hostPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("e2cp %s:%s: %v\n%s", device, guestPath, err, out)
	}
	data, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read copied file %q: %v", hostPath, err)
	}
	return data
}
