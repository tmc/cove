package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestGuestIPProbeArgsByGuestOS(t *testing.T) {
	if got, want := guestIPProbeArgs(false), []string{"ipconfig", "getifaddr", "en0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("guestIPProbeArgs(false) = %v, want %v", got, want)
	}
	got := guestIPProbeArgs(true)
	if len(got) != 3 || got[0] != "sh" || got[1] != "-lc" {
		t.Fatalf("guestIPProbeArgs(true) = %v, want shell probe", got)
	}
}

func TestParseGuestIPStripsCIDR(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "192.168.64.5\n", want: "192.168.64.5"},
		{name: "cidr", in: "192.168.64.5/24\n", want: "192.168.64.5"},
		{name: "empty", in: "\n", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseGuestIP(tt.in); got != tt.want {
				t.Fatalf("parseGuestIP(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCtlNetworkInfoBackfillsLinuxIPAndMAC(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	if err := os.WriteFile(filepath.Join(vmDir, "linux-disk.img"), nil, 0644); err != nil {
		t.Fatalf("write linux marker: %v", err)
	}
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "network-info",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"mode":"nat"}`,
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: guestIPProbeArgs(true),
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "192.168.64.9/24\n",
				}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"sh", "-lc", "cat /sys/class/net/eth0/address 2>/dev/null || true"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "aa:bb:cc:dd:ee:ff\n",
				}},
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return ctlNetworkInfoCommand(GetControlSocketPathForVM(vmDir), time.Second, false)
	})
	for _, want := range []string{
		`"guest_ip": "192.168.64.9"`,
		`"mac_address": "aa:bb:cc:dd:ee:ff"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(filepath.Join(vmDir, "mac.address"))
	if err != nil {
		t.Fatalf("read mac.address: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("mac.address = %q", got)
	}
}
