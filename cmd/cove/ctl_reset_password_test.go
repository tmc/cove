package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

// TestCtlResetPasswordOfflineRequiresVMDir guards the offline branch against
// an unresolved VM selection: with no directory it must fail with a clear
// error instead of resolving disk.img relative to the working directory.
func TestCtlResetPasswordOfflineRequiresVMDir(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "missing.sock")
	err := ctlResetPasswordForVM(vmSelection{}, sock, time.Second, "user", "pass")
	if err == nil {
		t.Fatal("ctlResetPasswordForVM() error = nil, want unresolved-directory error")
	}
	if !strings.Contains(err.Error(), "cannot resolve VM directory") {
		t.Fatalf("error = %v, want unresolved-directory diagnostic", err)
	}
}

// TestCtlResetPasswordResolvesVMRelativeDisk reproduces the live defect where
// `cove -vm <name> ctl reset-password` run from an unrelated directory failed
// with `vm disk not found: disk.img` — the disk path was resolved against the
// working directory instead of the selected VM's directory.
func TestCtlResetPasswordResolvesVMRelativeDisk(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)

	home := t.TempDir()
	vmDirectory := filepath.Join(home, ".vz", "vms", "pwreset-vm")
	if err := os.MkdirAll(vmDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	// A structurally valid macOS VM (disk.img + aux.img). The empty disk
	// cannot mount, so the command still fails — but past the disk lookup.
	for _, name := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(vmDirectory, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(bin, "-vm", "pwreset-vm", "ctl", "reset-password", "user", "pass")
	cmd.Dir = t.TempDir() // unrelated working directory
	cmd.Env = doctorE2EEnv(home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("reset-password on empty disk succeeded unexpectedly\noutput:\n%s", out)
	}
	if strings.Contains(string(out), "vm disk not found") {
		t.Fatalf("disk path resolved against working directory, not VM directory:\n%s", out)
	}
	if !strings.Contains(string(out), "mount data volume") {
		t.Fatalf("expected offline path to fail at mount stage\noutput:\n%s", out)
	}
}

func TestAutoLoginRefreshCommand(t *testing.T) {
	cmd, err := autoLoginRefreshCommand("testuser", "secret123")
	if err != nil {
		t.Fatalf("autoLoginRefreshCommand: %v", err)
	}
	if strings.Contains(cmd, "secret123") {
		t.Fatal("refresh command leaked the raw password")
	}
	for _, want := range []string{
		"base64 -D > /etc/kcpassword",
		"base64 -D > /Library/Preferences/com.apple.loginwindow.plist",
		"chown root:wheel /etc/kcpassword",
		"chown root:wheel /Library/Preferences/com.apple.loginwindow.plist",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("refresh command missing %q", want)
		}
	}
}

func TestRequireAgentExecSuccess(t *testing.T) {
	tests := []struct {
		name   string
		action string
		resp   *controlpb.ControlResponse
		want   string
	}{
		{
			name:   "agent exec success",
			action: "reset password",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			name:   "agent exec exit code",
			action: "reset password",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 10,
					Stderr:   "eDSAuthFailed\nsecond line",
				}},
			},
			want: "reset password: eDSAuthFailed",
		},
		{
			name:   "legacy data exit code",
			action: "refresh autologin artifacts",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"exitCode":7,"stdout":"","stderr":"permission denied\n"}`,
			},
			want: "refresh autologin artifacts: permission denied",
		},
		{
			name:   "response error",
			action: "reset password",
			resp: &controlpb.ControlResponse{
				Success: false,
				Error:   "agent unavailable",
			},
			want: "reset password: agent unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireAgentExecSuccess(tt.action, tt.resp)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("requireAgentExecSuccess: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("requireAgentExecSuccess returned nil error")
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("error = %q, want %q", got, tt.want)
			}
		})
	}
}
