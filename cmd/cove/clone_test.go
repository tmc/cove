package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestSupportsClonefile(t *testing.T) {
	dir := t.TempDir()

	// TempDir on macOS is typically APFS, so clonefile should work.
	got := SupportsClonefile(dir)
	if !got {
		t.Logf("clonefile not supported in %s (may be non-APFS)", dir)
	}
}

func TestSupportsClonefileBadDir(t *testing.T) {
	if SupportsClonefile("/nonexistent/dir") {
		t.Error("expected false for nonexistent directory")
	}
}

func TestCloneFileCreatesIdenticalContent(t *testing.T) {
	dir := t.TempDir()

	src := dir + "/src.txt"
	dst := dir + "/dst.txt"
	content := []byte("hello clonefile")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	if err := cloneFile(src, dst); err != nil {
		t.Skipf("clonefile not available: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("clone content = %q, want %q", got, content)
	}
}

func TestCloneVMLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(vmconfig.BaseDir(), "src-linux")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"linux-disk.img":   []byte("disk"),
		"linux-machine.id": []byte("machine"),
		"config.json":      []byte("{\"memoryGB\":4}\n"),
		"control.token":    []byte("token"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := CloneVM(CloneOptions{
		Source:        "src-linux",
		Target:        "dst-linux",
		CopyMachineID: true,
	}); err != nil {
		t.Fatalf("CloneVM() error = %v", err)
	}

	dst := filepath.Join(vmconfig.BaseDir(), "dst-linux")
	for _, name := range []string{"linux-disk.img", "linux-machine.id", "config.json", "control.token"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("cloned file %q missing: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "disk.img")); !os.IsNotExist(err) {
		t.Fatalf("unexpected macOS disk clone artifact: %v", err)
	}
}

func TestCloneVMUsesSourceDiskOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(vmconfig.BaseDir(), "src-macos")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"disk.img":    []byte("live-disk"),
		"aux.img":     []byte("aux"),
		"hw.model":    []byte("hw"),
		"machine.id":  []byte("machine"),
		"mac.address": []byte("aa:bb:cc:dd:ee:ff\n"),
		"config.json": []byte("{\"memoryGB\":4}\n"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	snapshotDir := filepath.Join(src, "disk-snapshots", "clean-base")
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		t.Fatal(err)
	}
	snapshotDisk := filepath.Join(snapshotDir, "disk.img")
	if err := os.WriteFile(snapshotDisk, []byte("snapshot-disk"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CloneVM(CloneOptions{
		Source:         "src-macos",
		Target:         "dst-macos",
		CopyMachineID:  true,
		SourceDiskPath: snapshotDisk,
	}); err != nil {
		t.Fatalf("CloneVM() error = %v", err)
	}

	dst := filepath.Join(vmconfig.BaseDir(), "dst-macos")
	got, err := os.ReadFile(filepath.Join(dst, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(dst disk): %v", err)
	}
	if string(got) != "snapshot-disk" {
		t.Fatalf("cloned disk = %q, want %q", got, "snapshot-disk")
	}
	got, err = os.ReadFile(filepath.Join(dst, "mac.address"))
	if err != nil {
		t.Fatalf("ReadFile(dst mac): %v", err)
	}
	if string(got) != "aa:bb:cc:dd:ee:ff\n" {
		t.Fatalf("cloned mac.address = %q, want source MAC", got)
	}
}

func TestCloneVMGeneratesFreshMAC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(vmconfig.BaseDir(), "src-macos")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"disk.img":    []byte("disk"),
		"aux.img":     []byte("aux"),
		"hw.model":    []byte("hw"),
		"machine.id":  []byte("machine"),
		"mac.address": []byte("aa:bb:cc:dd:ee:ff\n"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := CloneVM(CloneOptions{Source: "src-macos", Target: "dst-macos"}); err != nil {
		t.Fatalf("CloneVM() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(vmconfig.BaseDir(), "dst-macos", "mac.address"))
	if err != nil {
		t.Fatalf("ReadFile(dst mac): %v", err)
	}
	got := strings.TrimSpace(string(data))
	if _, err := net.ParseMAC(got); err != nil {
		t.Fatalf("generated mac.address = %q: %v", got, err)
	}
	if got == "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("generated mac.address reused source MAC %q", got)
	}
}

func TestRunCloneWithAgentProvisionFailureLeavesCloneSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(vmconfig.BaseDir(), "src-macos")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"disk.img":    []byte("disk"),
		"aux.img":     []byte("aux"),
		"hw.model":    []byte("hw"),
		"machine.id":  []byte("machine"),
		"config.json": []byte("{\"memoryGB\":4}\n"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	oldProvision := cloneProvisionAgentForVM
	cloneProvisionAgentForVM = func(vmSelection) error {
		return fmt.Errorf("native authorization requires an interactive terminal")
	}
	t.Cleanup(func() {
		cloneProvisionAgentForVM = oldProvision
	})

	stderr, restoreStderr := captureStderr(t)
	out, err := captureStdoutResult(t, func() error {
		return runClone([]string{"src-macos", "dst-macos", "--linked", "--with-agent"})
	})
	restoreStderr()
	if err != nil {
		t.Fatalf("runClone() error = %v", err)
	}
	for _, want := range []string{
		"Clone complete.",
		"Clone created: dst-macos",
		"=== Provisioning agent into clone ===",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q\n%s", want, out)
		}
	}
	for _, want := range []string{
		`warning: clone "dst-macos" was created, but --with-agent provisioning failed`,
		"native authorization requires an interactive terminal",
		"cove -vm dst-macos provision -agent",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q\n%s", want, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), "dst-macos", "disk.img")); err != nil {
		t.Fatalf("clone disk missing after provision warning: %v", err)
	}
}

func TestRunCloneWithAgentLinuxSkipIsNote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(vmconfig.BaseDir(), "src-linux-agent")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"linux-disk.img": []byte("disk"),
		"config.json":    []byte("{\"memoryGB\":4,\"os\":\"linux\"}\n"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	oldProvision := cloneProvisionAgentForVM
	cloneProvisionAgentForVM = func(vmSelection) error {
		return fmt.Errorf("offline agent inject is not supported for Linux VMs")
	}
	t.Cleanup(func() {
		cloneProvisionAgentForVM = oldProvision
	})

	stderr, restoreStderr := captureStderr(t)
	_, err := captureStdoutResult(t, func() error {
		return runClone([]string{"src-linux-agent", "dst-linux-agent", "--with-agent"})
	})
	restoreStderr()
	if err != nil {
		t.Fatalf("runClone() error = %v", err)
	}
	for _, want := range []string{
		`note: clone "dst-linux-agent" is Linux`,
		"keeps any agent already installed",
		"cove -vm dst-linux-agent provision-agent",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q\n%s", want, stderr.String())
		}
	}
}

func TestCloneVMSentinelErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := CloneVM(CloneOptions{Source: "missing-src", Target: "any-dst"}); !errors.Is(err, ErrCloneSourceNotFound) {
		t.Fatalf("missing source err = %v, want ErrCloneSourceNotFound", err)
	}

	src := filepath.Join(vmconfig.BaseDir(), "live-src")
	dst := filepath.Join(vmconfig.BaseDir(), "live-dst")
	for _, p := range []string{src, dst} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{
			"linux-disk.img":   []byte("d"),
			"linux-machine.id": []byte("m"),
			"config.json":      []byte("{}\n"),
		} {
			if err := os.WriteFile(filepath.Join(p, name), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := CloneVM(CloneOptions{Source: "live-src", Target: "live-dst"}); !errors.Is(err, ErrCloneTargetExists) {
		t.Fatalf("target exists err = %v, want ErrCloneTargetExists", err)
	}
}
