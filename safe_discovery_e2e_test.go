package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSafeDiscoveryNoResidueE2E(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr string
		notPath    string
	}{
		{
			name:       "bare noninteractive",
			wantExit:   0,
			wantStderr: "Usage:",
		},
		{
			name:       "bare run no default create",
			args:       []string{"run"},
			wantExit:   1,
			wantStderr: "run: no VM",
		},
		{
			name:       "config export help",
			args:       []string{"config", "export", "--help"},
			wantExit:   0,
			wantStderr: "Usage: cove vm config export <path>",
			notPath:    "--help",
		},
		{
			name:       "config import help",
			args:       []string{"config", "import", "--help"},
			wantExit:   0,
			wantStderr: "Usage: cove vm config import <path>",
			notPath:    "--help",
		},
		{
			name:       "config export dash path rejected",
			args:       []string{"config", "export", "-out.vzcfg"},
			wantExit:   1,
			wantStderr: "pass it after --",
			notPath:    "-out.vzcfg",
		},
		{
			name:       "image list json",
			args:       []string{"image", "list", "--json"},
			wantExit:   0,
			wantStdout: "[]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			stdout, stderr, gotExit := runCoveDiscoveryCommand(t, bin, home, tt.args...)
			if gotExit != tt.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", gotExit, tt.wantExit, stdout, stderr)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout, tt.wantStdout) {
				t.Fatalf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
				t.Fatalf("default VM dir stat = %v, want not exist", err)
			}
			if tt.notPath != "" {
				if _, err := os.Stat(filepath.Join(home, tt.notPath)); !os.IsNotExist(err) {
					t.Fatalf("%s stat = %v, want not exist", tt.notPath, err)
				}
			}
		})
	}
}

func runCoveDiscoveryCommand(t *testing.T, bin, home string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Dir = home
	cmd.Stdin = strings.NewReader("")
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("command timed out: %v", append([]string{bin}, args...))
	}
	if err == nil {
		return out.String(), errOut.String(), 0
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), errOut.String(), exitErr.ExitCode()
}
