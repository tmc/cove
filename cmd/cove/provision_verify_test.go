package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentstate "github.com/tmc/cove/internal/agent"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestAgentExecExitOK(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		err  error
		want bool
	}{
		{
			name: "exit zero",
			resp: agentExecVerifyResponse(0),
			want: true,
		},
		{
			name: "exit nonzero",
			resp: agentExecVerifyResponse(1),
			want: false,
		},
		{
			name: "transport success without exec result",
			resp: &controlpb.ControlResponse{Success: true},
			want: false,
		},
		{
			name: "control failure",
			resp: &controlpb.ControlResponse{Success: false, Error: "agent failed"},
			want: false,
		},
		{
			name: "send error",
			err:  errors.New("dial failed"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentExecExitOK(tt.resp, tt.err)
			if got != tt.want {
				t.Fatalf("agentExecExitOK() = %v, want %v", got, tt.want)
			}
		})
	}
}

func agentExecVerifyResponse(exitCode int32) *controlpb.ControlResponse {
	return &controlpb.ControlResponse{
		Success: true,
		Result: &controlpb.ControlResponse_AgentExecResult{
			AgentExecResult: &controlpb.AgentExecResponse{ExitCode: exitCode},
		},
	}
}

func TestVerifyRunningGuestProbesLinux(t *testing.T) {
	oldClipboard := enableClipboard
	enableClipboard = true
	t.Cleanup(func() { enableClipboard = oldClipboard })

	probes := verifyRunningGuestProbes(agentstate.PlatformLinux)
	descs := make([]string, 0, len(probes))
	for _, probe := range probes {
		descs = append(descs, probe.desc)
	}
	wantDescs := []string{
		"Agent binary",
		"Agent service",
		"Agent service status",
		"Provisioning completed marker",
		"vz-agent process",
		"Guest tools (clipboard)",
	}
	if !reflect.DeepEqual(descs, wantDescs) {
		t.Fatalf("probe descs = %#v, want %#v", descs, wantDescs)
	}
	for _, probe := range probes {
		if probe.desc == "Agent LaunchDaemon" {
			t.Fatalf("linux probes include macOS LaunchDaemon: %#v", probes)
		}
	}
	if got := probes[1].args; len(got) != 3 || got[0] != "sh" || got[1] != "-lc" ||
		!strings.Contains(got[2], "/etc/systemd/system/vz-agent.service") ||
		!strings.Contains(got[2], "/etc/init.d/vz-agent") {
		t.Fatalf("service args = %#v, want systemd/openrc shell probe", got)
	}
	if got := probes[2].args; len(got) != 3 || got[0] != "sh" || got[1] != "-lc" ||
		!strings.Contains(got[2], "systemctl is-active vz-agent") ||
		!strings.Contains(got[2], "rc-service vz-agent status") {
		t.Fatalf("service status args = %#v, want systemd/openrc shell probe", got)
	}
	if got := probes[3].args; len(got) != 3 || got[0] != "sh" || got[1] != "-lc" {
		t.Fatalf("marker args = %#v, want shell probe", got)
	}
	if got, want := probes[4].args, []string{"pgrep", "-f", "/usr/local/bin/vz-agent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("process args = %#v, want %#v", got, want)
	}
	if got := probes[5].args; len(got) != 3 || got[0] != "sh" || got[1] != "-lc" ||
		!strings.Contains(got[2], "command -v spice-vdagent") ||
		!strings.Contains(got[2], "test -f /usr/bin/spice-vdagent") ||
		!strings.Contains(got[2], "test -f /usr/local/bin/spice-vdagent") {
		t.Fatalf("guest tools args = %#v, want spice-vdagent probe", got)
	}
}

func TestVerifyRunningGuestProbesMacOS(t *testing.T) {
	oldClipboard := enableClipboard
	enableClipboard = true
	t.Cleanup(func() { enableClipboard = oldClipboard })

	probes := verifyRunningGuestProbes(agentstate.PlatformMacOS)
	var foundLaunchDaemon, foundGuestTools bool
	for _, probe := range probes {
		if probe.desc == "Agent LaunchDaemon" {
			foundLaunchDaemon = true
			if got, want := probe.args, []string{"test", "-f", "/Library/LaunchDaemons/com.tmc.cove.vz-agent.plist"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("launchdaemon args = %#v, want %#v", got, want)
			}
		}
		if probe.desc == "Guest tools (clipboard)" {
			foundGuestTools = true
			if got := probe.args; len(got) != 3 || got[0] != "sh" || got[1] != "-lc" ||
				!strings.Contains(got[2], "command -v spice-vdagent") ||
				!strings.Contains(got[2], "com.utmapp.spice-vdagentd.plist") ||
				!strings.Contains(got[2], "/usr/local/bin/spice-vdagent") {
				t.Fatalf("macOS guest tools args = %#v, want spice-vdagent probe", got)
			}
		}
	}
	if !foundLaunchDaemon {
		t.Fatal("macOS probes missing Agent LaunchDaemon")
	}
	if !foundGuestTools {
		t.Fatal("macOS probes missing Guest tools (clipboard)")
	}
}

func TestVerifyRunningGuestProbesClipboardDisabled(t *testing.T) {
	oldClipboard := enableClipboard
	enableClipboard = false
	t.Cleanup(func() { enableClipboard = oldClipboard })

	for _, platform := range []string{agentstate.PlatformLinux, agentstate.PlatformMacOS} {
		probes := verifyRunningGuestProbes(platform)
		for _, probe := range probes {
			if probe.desc == "Guest tools (clipboard)" {
				t.Fatalf("%s probes include clipboard probe when clipboard disabled", platform)
			}
		}
	}
}

func TestGuestToolsProbeReport(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int32
		err      error
		wantOK   bool
		wantLine string
	}{
		{
			name:     "present",
			exitCode: 0,
			wantOK:   true,
			wantLine: "+ Guest tools (clipboard): spice-vdagent present",
		},
		{
			name:     "missing exit 1",
			exitCode: 1,
			wantOK:   false,
			wantLine: "- Guest tools (clipboard): not found; clipboard sharing requires spice-vdagent (run 'cove provision -guest-tools')",
		},
		{
			name:     "exec error",
			exitCode: 1,
			err:      errors.New("dial error"),
			wantOK:   false,
			wantLine: "- Guest tools (clipboard): not found; clipboard sharing requires spice-vdagent (run 'cove provision -guest-tools')",
		},
	}

	probe := verifyRunningGuestProbe{
		desc:    "Guest tools (clipboard)",
		ok:      "spice-vdagent present",
		missing: "not found; clipboard sharing requires spice-vdagent (run 'cove provision -guest-tools')",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := agentExecVerifyResponse(tt.exitCode)
			if tt.err != nil {
				resp = nil
			}
			ok := agentExecExitOK(resp, tt.err)
			if ok != tt.wantOK {
				t.Fatalf("agentExecExitOK() = %v, want %v", ok, tt.wantOK)
			}
			var line string
			if ok {
				line = fmt.Sprintf("+ %s: %s", probe.desc, probe.ok)
			} else {
				line = fmt.Sprintf("- %s: %s", probe.desc, probe.missing)
			}
			if line != tt.wantLine {
				t.Fatalf("line = %q, want %q", line, tt.wantLine)
			}
		})
	}
}

func TestGuestToolsInstalledOnDisk(t *testing.T) {
	tests := []struct {
		name  string
		setup func(dir string)
		want  bool
	}{
		{
			name:  "empty dir",
			setup: func(dir string) {},
			want:  false,
		},
		{
			name: "package pending",
			setup: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "private", "var", "db"), 0755)
				os.WriteFile(filepath.Join(dir, "private", "var", "db", "vz-guest-tools.pkg"), []byte("pkg"), 0644)
			},
			want: true,
		},
		{
			name: "installed marker",
			setup: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "private", "var", "db"), 0755)
				os.WriteFile(filepath.Join(dir, "private", "var", "db", ".vz-guest-tools-installed"), []byte(""), 0644)
			},
			want: true,
		},
		{
			name: "launchdaemon plist",
			setup: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "Library", "LaunchDaemons"), 0755)
				os.WriteFile(filepath.Join(dir, "Library", "LaunchDaemons", "com.utmapp.spice-vdagentd.plist"), []byte(""), 0644)
			},
			want: true,
		},
		{
			name: "binary",
			setup: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "usr", "local", "bin"), 0755)
				os.WriteFile(filepath.Join(dir, "usr", "local", "bin", "spice-vdagent"), []byte(""), 0755)
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(dir)
			got := guestToolsInstalledOnDisk(dir)
			if got != tt.want {
				t.Fatalf("guestToolsInstalledOnDisk() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewVerifyFlagSetClipboard(t *testing.T) {
	oldClipboard := enableClipboard
	t.Cleanup(func() { enableClipboard = oldClipboard })

	fs, _, _, _, _ := newVerifyFlagSet()
	if err := fs.Parse([]string{"-clipboard=false"}); err != nil {
		t.Fatalf("parse -clipboard=false: %v", err)
	}
	if enableClipboard {
		t.Fatalf("enableClipboard = true, want false")
	}

	enableClipboard = false
	fs2, _, _, _, _ := newVerifyFlagSet()
	if err := fs2.Parse([]string{"-clipboard=true"}); err != nil {
		t.Fatalf("parse -clipboard=true: %v", err)
	}
	if !enableClipboard {
		t.Fatalf("enableClipboard = false, want true")
	}
}
