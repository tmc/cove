package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestWindowsQEMUControlServerStatusCapabilities(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "process.json"), []byte(`{"state":"stopped","qemuPid":987}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"vncEndpoint":"127.0.0.1:5907","vncURL":"vnc://127.0.0.1:5907"}`), 0644); err != nil {
		t.Fatal(err)
	}

	server, err := startWindowsQEMUControlServer(context.Background(), dir)
	if err != nil {
		t.Fatalf("startWindowsQEMUControlServer: %v", err)
	}
	t.Cleanup(server.Stop)

	resp, err := ctlSendRequest(server.SocketPath(), &controlpb.ControlRequest{Type: "status"}, time.Second, "status")
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	if !resp.Success || resp.GetStatus() == nil {
		t.Fatalf("status response = %#v", resp)
	}
	if resp.GetStatus().State != "stopped" {
		t.Fatalf("status state = %q, want stopped", resp.GetStatus().State)
	}
	var status windowsQEMUCTLStatus
	if err := json.Unmarshal([]byte(resp.Data), &status); err != nil {
		t.Fatalf("status data: %v", err)
	}
	if status.Backend != "qemu-hvf" || status.QEMUPID != 987 {
		t.Fatalf("status data = %#v", status)
	}

	resp, err = ctlSendRequest(server.SocketPath(), &controlpb.ControlRequest{Type: "capabilities"}, time.Second, "capabilities")
	if err != nil {
		t.Fatalf("capabilities request: %v", err)
	}
	capabilities := resp.GetCapabilities()
	if !resp.Success || capabilities == nil {
		t.Fatalf("capabilities response = %#v", resp)
	}
	for _, want := range []string{"status", "screenshot", "agent-exec", "agent-read", "pause", "snapshot"} {
		if !qemuControlContainsString(capabilities.Commands, want) {
			t.Fatalf("capabilities commands missing %q: %v", want, capabilities.Commands)
		}
	}
	if !capabilities.Features["qemu"] || capabilities.Features["snapshots"] {
		t.Fatalf("capabilities features = %#v", capabilities.Features)
	}
}

func TestWindowsQEMUControlServerUnsupportedNativeCommands(t *testing.T) {
	handler := &windowsQEMUControlHandler{vmDir: t.TempDir()}
	for _, cmd := range []string{"pause", "resume", "snapshot"} {
		resp := handler.Handle(&controlpb.ControlRequest{Type: cmd})
		if resp.Success || !strings.Contains(resp.Error, "not supported for qemu windows VMs") {
			t.Fatalf("%s response = %#v", cmd, resp)
		}
	}
}

func TestWindowsQEMUControlServerAgentCopyIsRecognized(t *testing.T) {
	handler := &windowsQEMUControlHandler{vmDir: t.TempDir()}
	resp := handler.Handle(&controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{AgentCp: &controlpb.AgentCopyCommand{
			HostPath:  "/tmp/app.log",
			GuestPath: "/C:/Users/cove/Desktop/app.log",
			ToGuest:   true,
		}},
	})
	if strings.Contains(resp.Error, "not supported") {
		t.Fatalf("agent-cp returned unsupported: %#v", resp)
	}
	if !strings.Contains(resp.Error, "qemu agent endpoint is empty") {
		t.Fatalf("agent-cp response = %#v, want missing endpoint error", resp)
	}
}

func TestQEMUKeySpecFromControl(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  *controlpb.KeyCommand
		want string
	}{
		{name: "return keycode", cmd: &controlpb.KeyCommand{KeyCode: 36, KeyDown: true}, want: "ret"},
		{name: "a keycode", cmd: &controlpb.KeyCommand{KeyCode: 0, KeyDown: true}, want: "a"},
		{name: "digit keycode", cmd: &controlpb.KeyCommand{KeyCode: 29, KeyDown: true}, want: "0"},
		{name: "shift modifier", cmd: &controlpb.KeyCommand{KeyCode: 0, Modifiers: 1 << 17, KeyDown: true}, want: "shift-a"},
		{name: "character", cmd: &controlpb.KeyCommand{Character: "Z", KeyDown: true}, want: "shift-z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := qemuKeySpecFromControl(tt.cmd)
			if err != nil {
				t.Fatalf("qemuKeySpecFromControl: %v", err)
			}
			if got != tt.want {
				t.Fatalf("qemuKeySpecFromControl = %q, want %q", got, tt.want)
			}
		})
	}
}

func qemuControlContainsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
