package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestConfirmDeletefUnit(t *testing.T) {
	oldIsTerminal := confirmStdinIsTerminal
	oldStdin := confirmStdin
	oldStderr := confirmStderr
	oldExit := confirmExit
	t.Cleanup(func() {
		confirmStdinIsTerminal = oldIsTerminal
		confirmStdin = oldStdin
		confirmStderr = oldStderr
		confirmExit = oldExit
	})

	tests := []struct {
		name           string
		isTerminal     bool
		input          string
		wantExitCode   int
		wantCalledExit bool
		wantStderr     string
		wantOK         bool
		wantErr        bool
	}{
		{
			name:           "non-interactive fails with exit 2",
			isTerminal:     false,
			wantCalledExit: true,
			wantExitCode:   2,
			wantStderr:     "deletion requires confirmation; use -y/--yes in non-interactive environments",
			wantOK:         false,
			wantErr:        true,
		},
		{
			name:           "interactive answer y confirms",
			isTerminal:     true,
			input:          "y\n",
			wantCalledExit: false,
			wantStderr:     "",
			wantOK:         true,
			wantErr:        false,
		},
		{
			name:           "interactive answer Y confirms",
			isTerminal:     true,
			input:          "Y\n",
			wantCalledExit: false,
			wantStderr:     "",
			wantOK:         true,
			wantErr:        false,
		},
		{
			name:           "interactive answer n aborts with exit 1",
			isTerminal:     true,
			input:          "n\n",
			wantCalledExit: true,
			wantExitCode:   1,
			wantStderr:     "aborted",
			wantOK:         false,
			wantErr:        true,
		},
		{
			name:           "interactive answer no aborts with exit 1",
			isTerminal:     true,
			input:          "no\n",
			wantCalledExit: true,
			wantExitCode:   1,
			wantStderr:     "aborted",
			wantOK:         false,
			wantErr:        true,
		},
		{
			name:           "interactive empty input aborts with exit 1",
			isTerminal:     true,
			input:          "\n",
			wantCalledExit: true,
			wantExitCode:   1,
			wantStderr:     "aborted",
			wantOK:         false,
			wantErr:        true,
		},
		{
			name:           "interactive EOF aborts with exit 1",
			isTerminal:     true,
			input:          "",
			wantCalledExit: true,
			wantExitCode:   1,
			wantStderr:     "aborted",
			wantOK:         false,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confirmStdinIsTerminal = func() bool { return tt.isTerminal }
			confirmStdin = strings.NewReader(tt.input)
			var errBuf bytes.Buffer
			confirmStderr = &errBuf

			var gotExitCode int
			var calledExit bool
			confirmExit = func(code int) {
				calledExit = true
				gotExitCode = code
			}

			ok, err := confirmDeletef("Delete item? [y/N] ")
			if (err != nil) != tt.wantErr {
				t.Errorf("confirmDeletef() err = %v, wantErr = %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Errorf("confirmDeletef() ok = %v, wantOK = %v", ok, tt.wantOK)
			}
			if calledExit != tt.wantCalledExit {
				t.Errorf("confirmExit called = %v, want %v", calledExit, tt.wantCalledExit)
			}
			if calledExit && gotExitCode != tt.wantExitCode {
				t.Errorf("confirmExit code = %d, want %d", gotExitCode, tt.wantExitCode)
			}
			if tt.wantStderr != "" && !strings.Contains(errBuf.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", errBuf.String(), tt.wantStderr)
			}
		})
	}
}

func TestDeleteNonInteractiveE2E(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)

	nonInteractiveCases := []struct {
		name string
		args []string
	}{
		{"rm without yes", []string{"rm", "demo-vm"}},
		{"vm delete without yes", []string{"vm", "delete", "demo-vm"}},
		{"snapshot delete without yes", []string{"snapshot", "delete", "snap1"}},
		{"disk-snapshot delete without yes", []string{"disk-snapshot", "delete", "snap1"}},
		{"disk-snapshot restore without yes", []string{"disk-snapshot", "restore", "snap1"}},
		{"pit delete without yes", []string{"pit", "delete", "snap1"}},
		{"clean without yes", []string{"clean"}},
	}

	for _, tc := range nonInteractiveCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			writeTreeVM(t, "demo-vm", vmconfig.Config{})
			if err := vmconfig.SetActive("demo-vm"); err != nil {
				t.Fatalf("SetActive: %v", err)
			}
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = doctorE2EEnv(home)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("expected command to fail without -y in non-interactive environment")
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("unexpected error type: %v", err)
			}
			if exitErr.ExitCode() != 2 {
				t.Fatalf("exit code = %d, want 2\nstderr:\n%s", exitErr.ExitCode(), stderr.String())
			}
			wantMsg := "deletion requires confirmation; use -y/--yes in non-interactive environments"
			if !strings.Contains(stderr.String(), wantMsg) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), wantMsg)
			}
		})
	}

	t.Run("rm with -y deletes VM", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeTreeVM(t, "victim-vm", vmconfig.Config{})
		vmDir := filepath.Join(home, ".vz", "vms", "victim-vm")
		if _, err := os.Stat(vmDir); err != nil {
			t.Fatalf("setup victim VM dir: %v", err)
		}

		cmd := exec.Command(bin, "rm", "-y", "victim-vm")
		cmd.Env = doctorE2EEnv(home)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("rm -y failed: %v\nstderr: %s", err, stderr.String())
		}
		if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
			t.Fatalf("expected VM dir to be deleted, stat err = %v", err)
		}
	})

	t.Run("vm delete with --yes deletes VM", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeTreeVM(t, "victim-vm2", vmconfig.Config{})
		vmDir := filepath.Join(home, ".vz", "vms", "victim-vm2")
		if _, err := os.Stat(vmDir); err != nil {
			t.Fatalf("setup victim VM dir: %v", err)
		}

		cmd := exec.Command(bin, "vm", "delete", "--yes", "victim-vm2")
		cmd.Env = doctorE2EEnv(home)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("vm delete --yes failed: %v\nstderr: %s", err, stderr.String())
		}
		if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
			t.Fatalf("expected VM dir to be deleted, stat err = %v", err)
		}
	})

	t.Run("disk-snapshot delete with -y deletes snapshot", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeTreeVM(t, "snap-vm", vmconfig.Config{})
		snapDir := filepath.Join(home, ".vz", "vms", "snap-vm", "disk-snapshots", "snap-1")
		if err := os.MkdirAll(snapDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(snapDir, "metadata.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(bin, "-vm", "snap-vm", "disk-snapshot", "delete", "-y", "snap-1")
		cmd.Env = doctorE2EEnv(home)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("disk-snapshot delete -y failed: %v\nstderr: %s", err, stderr.String())
		}
		if _, err := os.Stat(snapDir); !os.IsNotExist(err) {
			t.Fatalf("expected snapshot dir to be deleted, stat err = %v", err)
		}
	})
}
