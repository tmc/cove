package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestCompactCommand(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		want     []string
		wantErr  string
	}{
		{name: "linux", platform: agentstate.PlatformLinux, want: []string{"fstrim", "-v", "/"}},
		{name: "macos", platform: agentstate.PlatformMacOS, want: []string{"sh", "-c", macOSCompactScript}},
		{name: "unknown", platform: "windows", wantErr: "unsupported guest platform"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := compactCommand(tt.platform)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("compactCommand() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("compactCommand(): %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("compactCommand() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCompactVMWithClient(t *testing.T) {
	vmPath := makeTestVMDir(t)
	if err := vmconfig.Save(vmPath, &vmconfig.Config{Agent: &vmconfig.AgentConfig{Platform: agentstate.PlatformLinux}}); err != nil {
		t.Fatalf("vmconfig.Save() error = %v", err)
	}

	client := &fakeCompactClient{
		execResp: &controlpb.AgentExecResponse{
			ExitCode: 0,
			Stdout:   "trimmed\n",
		},
	}
	got, err := compactVMWithClient(vmPath, client)
	if err != nil {
		t.Fatalf("compactVMWithClient(): %v", err)
	}
	if !client.pinged {
		t.Fatal("compactVMWithClient() did not ping agent")
	}
	if want := []string{"fstrim", "-v", "/"}; !reflect.DeepEqual(client.execArgs, want) {
		t.Fatalf("compactVMWithClient() args = %#v, want %#v", client.execArgs, want)
	}
	if client.execTimeout != compactTimeout {
		t.Fatalf("compactVMWithClient() timeout = %s, want %s", client.execTimeout, compactTimeout)
	}
	if got.Platform != agentstate.PlatformLinux || got.Stdout != "trimmed" {
		t.Fatalf("compactVMWithClient() = %#v", got)
	}
}

func TestCompactVMWithClientPingFailure(t *testing.T) {
	errPing := errors.New("offline")
	_, err := compactVMWithClient(makeTestVMDir(t), &fakeCompactClient{pingErr: errPing})
	if !errors.Is(err, errPing) || !strings.Contains(err.Error(), "guest agent unavailable") {
		t.Fatalf("compactVMWithClient() error = %v, want guest agent unavailable wrapping ping error", err)
	}
}

func TestCompactVMWithClientExitFailure(t *testing.T) {
	client := &fakeCompactClient{
		execResp: &controlpb.AgentExecResponse{
			ExitCode: 2,
			Stderr:   "not enough space\n",
		},
	}
	_, err := compactVMWithClient(makeTestVMDir(t), client)
	if err == nil || !strings.Contains(err.Error(), "exit 2: not enough space") {
		t.Fatalf("compactVMWithClient() error = %v, want exit stderr", err)
	}
}

func TestVMAgentPlatformUsesConfig(t *testing.T) {
	vmPath := makeTestVMDir(t)
	if err := vmconfig.Save(vmPath, &vmconfig.Config{Agent: &vmconfig.AgentConfig{Platform: agentstate.PlatformLinux}}); err != nil {
		t.Fatalf("vmconfig.Save() error = %v", err)
	}
	if got := agentstate.Platform(vmPath); got != agentstate.PlatformLinux {
		t.Fatalf("Platform() = %q, want %q", got, agentstate.PlatformLinux)
	}
}

type fakeCompactClient struct {
	pinged      bool
	pingErr     error
	execArgs    []string
	execTimeout time.Duration
	execResp    *controlpb.AgentExecResponse
	execErr     error
}

func (c *fakeCompactClient) AgentPingTyped() (string, error) {
	c.pinged = true
	return "test", c.pingErr
}

func (c *fakeCompactClient) AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	c.execArgs = append([]string(nil), args...)
	c.execTimeout = timeout
	return c.execResp, c.execErr
}
