package main

import (
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestHandlePortForwardValidation(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())

	tests := []struct {
		name    string
		cmd     *controlpb.PortForwardCommand
		wantErr string
	}{
		{"start missing ports", &controlpb.PortForwardCommand{Action: "start"}, "host_port and guest_port required"},
		{"start-udp missing ports", &controlpb.PortForwardCommand{Action: "start-udp"}, "host_port and guest_port required"},
		{"start-reverse missing ports", &controlpb.PortForwardCommand{Action: "start-reverse"}, "host_port and guest_port required"},
		{"start-reverse-udp missing ports", &controlpb.PortForwardCommand{Action: "start-reverse-udp"}, "host_port and guest_port required"},
		{"stop missing port", &controlpb.PortForwardCommand{Action: "stop"}, "host_port required"},
		{"unknown action", &controlpb.PortForwardCommand{Action: "bogus"}, "unknown port-forward action: bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := s.handlePortForward(tt.cmd)
			if resp.Error != tt.wantErr {
				t.Fatalf("Error = %q, want %q", resp.Error, tt.wantErr)
			}
			if resp.Success {
				t.Fatalf("unexpected Success=true")
			}
		})
	}
}

func TestHandlePortForwardListEmpty(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handlePortForward(&controlpb.PortForwardCommand{Action: "list"})
	if !resp.Success {
		t.Fatalf("Success = false, error = %q", resp.Error)
	}
	if !strings.Contains(resp.Data, "no active port forwards") {
		t.Fatalf("Data = %q, want contains 'no active port forwards'", resp.Data)
	}
}

func TestHandlePortForwardStopNotFound(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handlePortForward(&controlpb.PortForwardCommand{Action: "stop", HostPort: 18080})
	if resp.Success {
		t.Fatalf("Success = true, want failure")
	}
	if !strings.Contains(resp.Error, "no forward on host port 18080") {
		t.Fatalf("Error = %q, want 'no forward on host port 18080'", resp.Error)
	}
}

func TestPortForwardSpecsSetAndString(t *testing.T) {
	var specs portForwardSpecs
	if got := specs.String(); got != "" {
		t.Fatalf("empty String = %q, want empty", got)
	}
	if err := specs.Set("8080:80"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := specs.Set("9090:9000"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, want := specs.String(), "8080:80,9090:9000"; got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
	if err := specs.Set("invalid"); err == nil {
		t.Fatal("Set(invalid) succeeded, want error")
	}
	if len(specs) != 2 {
		t.Fatalf("len = %d after invalid Set, want 2", len(specs))
	}
}

func TestJoinLines(t *testing.T) {
	if got := joinLines(nil); got != "" {
		t.Fatalf("joinLines(nil) = %q, want empty", got)
	}
	got := joinLines([]string{"a", "b"})
	want := "  a\n  b\n"
	if got != want {
		t.Fatalf("joinLines = %q, want %q", got, want)
	}
}
