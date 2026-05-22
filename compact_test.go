package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestPrintCompactResult(t *testing.T) {
	tests := []struct {
		name   string
		result *compactResult
		want   []string
		skip   []string
	}{
		{
			name:   "platform only",
			result: &compactResult{Platform: agentstate.PlatformLinux},
			want:   []string{"Compacted linux guest free space"},
			skip:   []string{"\n\n"},
		},
		{
			name:   "with stdout and stderr",
			result: &compactResult{Platform: agentstate.PlatformMacOS, Stdout: "ok", Stderr: "warn"},
			want:   []string{"Compacted macos guest free space", "ok\n", "warn\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printCompactResult(&buf, tt.result)
			got := buf.String()
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Fatalf("printCompactResult() = %q, want substring %q", got, w)
				}
			}
			for _, s := range tt.skip {
				if strings.Contains(got, s) {
					t.Fatalf("printCompactResult() = %q, must not contain %q", got, s)
				}
			}
		})
	}
}

func TestNewCompactFlagSet(t *testing.T) {
	var buf bytes.Buffer
	fs, target := newCompactFlagSet(&buf)
	if err := fs.Parse([]string{"-vm", "alpha"}); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if *target != "alpha" {
		t.Fatalf("target = %q, want alpha", *target)
	}
	// Usage writes to the supplied writer.
	fs.Usage()
	if buf.Len() == 0 {
		t.Fatal("Usage() wrote nothing to provided writer")
	}
}

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

func TestPrecheckCompactCapacityLinuxSkips(t *testing.T) {
	// Linux uses fstrim in-place; precheck should be a no-op even with no
	// disk.img present.
	if err := precheckCompactCapacity(t.TempDir(), agentstate.PlatformLinux); err != nil {
		t.Fatalf("precheckCompactCapacity(linux) = %v, want nil", err)
	}
}

func TestPrecheckCompactCapacityMissingDisk(t *testing.T) {
	err := precheckCompactCapacity(t.TempDir(), agentstate.PlatformMacOS)
	if err == nil || !strings.Contains(err.Error(), "disk.img") {
		t.Fatalf("precheckCompactCapacity(missing disk) = %v, want disk.img error", err)
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
