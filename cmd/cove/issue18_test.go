package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIssue18HelpStdout(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	tests := []struct {
		name           string
		args           []string
		wantExit       int
		wantOut        string
		wantZeroStderr bool
	}{
		{
			name:           "bare cove -h",
			args:           []string{"-h"},
			wantExit:       0,
			wantOut:        "Usage:\n  cove [flags] [command]",
			wantZeroStderr: true,
		},
		{
			name:           "bare cove --help",
			args:           []string{"--help"},
			wantExit:       0,
			wantOut:        "Usage:\n  cove [flags] [command]",
			wantZeroStderr: true,
		},
		{
			name:           "cove help",
			args:           []string{"help"},
			wantExit:       0,
			wantOut:        "Usage:\n  cove [flags] [command]",
			wantZeroStderr: true,
		},
		{
			name:           "cove help storage",
			args:           []string{"help", "storage"},
			wantExit:       0,
			wantOut:        "Usage: cove storage",
			wantZeroStderr: true,
		},
		{
			name:           "cove storage -h",
			args:           []string{"storage", "-h"},
			wantExit:       0,
			wantOut:        "Usage: cove storage",
			wantZeroStderr: true,
		},
		{
			name:           "cove runs -h",
			args:           []string{"runs", "-h"},
			wantExit:       0,
			wantOut:        "Usage: cove runs",
			wantZeroStderr: true,
		},
		{
			name:           "cove ctl -h",
			args:           []string{"ctl", "-h"},
			wantExit:       0,
			wantOut:        "Usage: cove ctl",
			wantZeroStderr: true,
		},
		{
			name:           "cove invalid usage exit 2",
			args:           []string{"storage", "bogussubcmd"},
			wantExit:       2,
			wantOut:        "",
			wantZeroStderr: false,
		},
		{
			name:           "cove pin no args exit 2",
			args:           []string{"pin"},
			wantExit:       2,
			wantOut:        "",
			wantZeroStderr: false,
		},
		{
			name:           "cove unpin no args exit 2",
			args:           []string{"unpin"},
			wantExit:       2,
			wantOut:        "",
			wantZeroStderr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = doctorE2EEnv(home)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()

			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("unexpected execution error: %v", err)
				}
			}

			if exitCode != tc.wantExit {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", exitCode, tc.wantExit, stdout.String(), stderr.String())
			}
			if tc.wantOut != "" && !strings.Contains(stdout.String(), tc.wantOut) {
				t.Fatalf("stdout missing %q\nstdout:\n%s\nstderr:\n%s", tc.wantOut, stdout.String(), stderr.String())
			}
			if tc.wantZeroStderr && stderr.Len() > 0 {
				t.Fatalf("expected empty stderr, got:\n%s", stderr.String())
			}
		})
	}
}

func TestIssue18VerifySingleUsageHeader(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	cmd := exec.Command(bin, "verify", "-h")
	cmd.Env = doctorE2EEnv(home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run verify -h: %v\nstderr:\n%s", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `Note: "verify" is a deprecated alias for "doctor".`) {
		t.Fatalf("missing deprecation note:\n%s", out)
	}
	if !strings.Contains(out, "Usage: cove doctor [options]") {
		t.Fatalf("missing doctor usage header:\n%s", out)
	}
	if strings.Contains(out, "Usage: cove verify [options]") {
		t.Fatalf("found duplicate verify usage header:\n%s", out)
	}
	usageCount := strings.Count(out, "Usage:")
	if usageCount != 1 {
		t.Fatalf("expected exactly 1 'Usage:' line, found %d in:\n%s", usageCount, out)
	}
}

func TestIssue18GuiHeadlessMutualExclusion(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	cases := [][]string{
		{"run", "-gui", "-headless"},
		{"run", "-headless", "-gui"},
		{"run", "somevm", "-gui", "-headless"},
		{"run", "somevm", "-headless", "-gui"},
	}

	for _, args := range cases {
		cmd := exec.Command(bin, args...)
		cmd.Env = doctorE2EEnv(home)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatalf("expected error for %v, got success", args)
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("expected exit code 2 for %v, got %v\nstderr: %s", args, err, stderr.String())
		}
		if !strings.Contains(stderr.String(), "-gui and -headless are mutually exclusive") {
			t.Fatalf("stderr missing mutual exclusion message: %s", stderr.String())
		}
	}
}

func TestIssue18HandleDefaultActionNoVMs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out := captureStdout(t, func() error {
		handleDefaultAction()
		return nil
	})
	if !strings.Contains(out, "cove doctor host") || !strings.Contains(out, "cove up -user <name>") {
		t.Fatalf("handleDefaultAction() output did not contain first-run usage:\n%s", out)
	}
	// Verify no download or install was initiated (no vms dir or images created).
	vmsDir := filepath.Join(home, ".vz", "vms")
	if entries, err := os.ReadDir(vmsDir); err == nil && len(entries) > 0 {
		t.Fatalf("expected no VMs created, found %d", len(entries))
	}
}

func TestIssue18CtlSocketResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldName, oldDir := vmName, vmDir
	vmName, vmDir = "", ""
	t.Cleanup(func() {
		vmName, vmDir = oldName, oldDir
	})

	vmDir := filepath.Join(home, ".vz", "vms", "mock-vm")
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "linux-disk.img"), []byte("mock"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Without -vm, resolves mock-vm as the single installed VM.
	err := ctlCommand([]string{"screenshot"})
	if err == nil {
		t.Fatal("ctlCommand(screenshot) succeeded on stopped VM, want error")
	}
	errStr := err.Error()
	if strings.Contains(errStr, "at control.sock") {
		t.Fatalf("error still contains bare relative 'at control.sock': %s", errStr)
	}
	if !strings.Contains(errStr, "vm is not running") {
		t.Fatalf("error %q does not contain 'vm is not running'", errStr)
	}
	if !strings.Contains(errStr, "mock-vm") {
		t.Fatalf("error %q does not contain VM name 'mock-vm'", errStr)
	}
	if !strings.Contains(errStr, "cove -vm mock-vm run") {
		t.Fatalf("error %q does not contain run hint with VM name", errStr)
	}

	// 2. With -vm mock-vm, resolves mock-vm explicitly.
	err = ctlCommand([]string{"-vm", "mock-vm", "screenshot"})
	if err == nil {
		t.Fatal("ctlCommand(-vm mock-vm screenshot) succeeded on stopped VM, want error")
	}
	errStr = err.Error()
	if strings.Contains(errStr, "at control.sock") {
		t.Fatalf("error still contains bare relative 'at control.sock': %s", errStr)
	}
	if !strings.Contains(errStr, "vm is not running") {
		t.Fatalf("error %q does not contain 'vm is not running'", errStr)
	}
	if !strings.Contains(errStr, "mock-vm") {
		t.Fatalf("error %q does not contain VM name 'mock-vm'", errStr)
	}
	if !strings.Contains(errStr, "cove -vm mock-vm run") {
		t.Fatalf("error %q does not contain run hint with VM name", errStr)
	}
}
