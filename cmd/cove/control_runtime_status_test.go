package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestControlServerRuntimeStatusCommands(t *testing.T) {
	dir := t.TempDir()
	sock := shortTestSocketPath(t)
	defer os.Remove(sock)

	s := NewControlServerWithVMDir(sock, dir)
	s.SetVNCStatus(VNCStatus{
		Enabled:           true,
		Port:              5901,
		Endpoint:          "127.0.0.1:5901",
		State:             "running",
		PasswordProtected: true,
		ServiceName:       "cove",
		Description:       "VNC server ready",
	})
	s.SetDebugStubStatus(DebugStubStatus{
		Enabled:     true,
		Kind:        "gdb",
		Port:        1234,
		Endpoint:    "0.0.0.0:1234",
		Connect:     "lldb -o 'gdb-remote 127.0.0.1:1234'",
		ListenAll:   true,
		State:       "attached",
		Description: "GDB debug stub attached",
	})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	waitForControlSocket(t, sock)

	resp := sendControlLine(t, sock, `{"type":"vnc-status","auth_token":"`+s.authToken+`"}`)
	if !resp.Success {
		t.Fatalf("vnc-status failed: %s", resp.Error)
	}
	var vncStatus VNCStatus
	if err := json.Unmarshal([]byte(resp.Data), &vncStatus); err != nil {
		t.Fatalf("unmarshal vnc status: %v", err)
	}
	if !vncStatus.Enabled || vncStatus.Port != 5901 || vncStatus.State != "running" || !vncStatus.PasswordProtected || vncStatus.Endpoint == "" {
		t.Fatalf("vnc status = %#v", vncStatus)
	}

	resp = sendControlLine(t, sock, `{"type":"debug-stub-status","auth_token":"`+s.authToken+`"}`)
	if !resp.Success {
		t.Fatalf("debug-stub-status failed: %s", resp.Error)
	}
	var debugStatus DebugStubStatus
	if err := json.Unmarshal([]byte(resp.Data), &debugStatus); err != nil {
		t.Fatalf("unmarshal debug status: %v", err)
	}
	if !debugStatus.Enabled || debugStatus.Kind != "gdb" || debugStatus.Port != 1234 || !debugStatus.ListenAll || debugStatus.Connect == "" {
		t.Fatalf("debug status = %#v", debugStatus)
	}

	resp = sendControlLine(t, sock, `{"type":"server-info","auth_token":"`+s.authToken+`"}`)
	if !resp.Success {
		t.Fatalf("server-info failed: %s", resp.Error)
	}
	var serverInfo RuntimeServerInfo
	if err := json.Unmarshal([]byte(resp.Data), &serverInfo); err != nil {
		t.Fatalf("unmarshal server info: %v", err)
	}
	if serverInfo.PID == 0 || serverInfo.Executable == "" || serverInfo.Commit == "" || serverInfo.SocketPath != sock {
		t.Fatalf("server info = %#v", serverInfo)
	}
	if serverInfo.Command == "" || serverInfo.StartSource == "" {
		t.Fatalf("server info missing owner context: %#v", serverInfo)
	}
}

func TestCtlCommandRuntimeStatus(t *testing.T) {
	dir := t.TempDir()
	sock := shortTestSocketPath(t)
	defer os.Remove(sock)

	s := NewControlServerWithVMDir(sock, dir)
	s.SetVNCStatus(VNCStatus{Enabled: true, Port: 5901, Endpoint: "127.0.0.1:5901", State: "running"})
	s.SetDebugStubStatus(DebugStubStatus{Enabled: true, Kind: "gdb", Port: 1234, Endpoint: "0.0.0.0:1234", Connect: "lldb -o 'gdb-remote 127.0.0.1:1234'", ListenAll: true})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	waitForControlSocket(t, sock)

	vncOut := captureStdout(t, func() error {
		return ctlCommand([]string{"-socket", sock, "-token", s.authToken, "vnc", "status"})
	})
	if !strings.Contains(vncOut, `"enabled": true`) || !strings.Contains(vncOut, `"port": 5901`) {
		t.Fatalf("ctl vnc status output = %q", vncOut)
	}
	if !strings.Contains(vncOut, `"endpoint": "127.0.0.1:5901"`) {
		t.Fatalf("ctl vnc status missing endpoint: %q", vncOut)
	}

	debugOut := captureStdout(t, func() error {
		return ctlCommand([]string{"-socket", sock, "-token", s.authToken, "debug-stub", "status"})
	})
	if !strings.Contains(debugOut, `"kind": "gdb"`) || !strings.Contains(debugOut, `"listen_all": true`) {
		t.Fatalf("ctl debug-stub status output = %q", debugOut)
	}
	if !strings.Contains(debugOut, `"connect": "lldb -o 'gdb-remote 127.0.0.1:1234'"`) {
		t.Fatalf("ctl debug-stub status missing connect hint: %q", debugOut)
	}

	serverOut := captureStdout(t, func() error {
		return ctlCommand([]string{"-socket", sock, "-token", s.authToken, "server-info"})
	})
	if !strings.Contains(serverOut, `"pid":`) || !strings.Contains(serverOut, `"socket_path": "`+sock+`"`) {
		t.Fatalf("ctl server-info output = %q", serverOut)
	}
}

func TestControlServerCapabilitiesIncludeRuntimeStatusCommands(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.getCapabilities()
	if !resp.Success {
		t.Fatalf("capabilities failed: %s", resp.Error)
	}
	caps := resp.GetCapabilities()
	if caps == nil {
		t.Fatalf("missing capabilities result: %#v", resp)
	}
	wantCommands := map[string]bool{
		"vnc-status":        false,
		"debug-stub-status": false,
		"server-info":       false,
		"disk":              false,
		"usb":               false,
	}
	for _, cmd := range caps.Commands {
		if _, ok := wantCommands[cmd]; ok {
			wantCommands[cmd] = true
		}
	}
	for cmd, ok := range wantCommands {
		if !ok {
			t.Fatalf("capabilities missing %q", cmd)
		}
	}
	if !caps.Features["vncStatus"] || !caps.Features["debugStubStatus"] {
		t.Fatalf("capabilities missing runtime status features: %#v", caps.Features)
	}
	if !caps.Features["runtimeDiskControl"] || !caps.Features["runtimeUSBControl"] {
		t.Fatalf("capabilities missing runtime device features: %#v", caps.Features)
	}
}

func TestControlServerCapabilitiesFilterGuestOSCommands(t *testing.T) {
	oldLinux, oldWindows := linuxMode, windowsMode
	defer func() {
		linuxMode = oldLinux
		windowsMode = oldWindows
	}()

	tests := []struct {
		name         string
		linux        bool
		windows      bool
		wantRecovery bool
	}{
		{name: "macos", wantRecovery: true},
		{name: "linux", linux: true},
		{name: "windows", windows: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			linuxMode = tt.linux
			windowsMode = tt.windows

			resp := NewControlServerWithVMDir("", t.TempDir()).getCapabilities()
			caps := resp.GetCapabilities()
			if caps == nil {
				t.Fatalf("missing capabilities result: %#v", resp)
			}
			if got := stringSliceContains(caps.Commands, "reboot-to-recovery"); got != tt.wantRecovery {
				t.Fatalf("typed reboot-to-recovery present = %v, want %v in %#v", got, tt.wantRecovery, caps.Commands)
			}

			var payload struct {
				Commands []string        `json:"commands"`
				Features map[string]bool `json:"features"`
			}
			if err := json.Unmarshal([]byte(resp.Data), &payload); err != nil {
				t.Fatalf("unmarshal json payload: %v", err)
			}
			if got := stringSliceContains(payload.Commands, "reboot-to-recovery"); got != tt.wantRecovery {
				t.Fatalf("json reboot-to-recovery present = %v, want %v in %#v", got, tt.wantRecovery, payload.Commands)
			}
			if !payload.Features["runtimeDiskControl"] || !caps.Features["runtimeDiskControl"] {
				t.Fatalf("features not preserved: typed=%#v json=%#v", caps.Features, payload.Features)
			}
		})
	}
}

func stringSliceContains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan error, 1)
	go func() {
		done <- fn()
		_ = w.Close()
	}()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("ctl command: %v", err)
	}
	return buf.String()
}

func TestControlRuntimeStatusDefaults(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	if got := s.VNCStatus(); got.Enabled || got.State != "disabled" {
		t.Fatalf("default vnc status = %#v", got)
	}
	if got := s.DebugStubStatus(); got.Enabled || got.State != "disabled" {
		t.Fatalf("default debug status = %#v", got)
	}
}

func TestControlRuntimeStatusRoundTripJSON(t *testing.T) {
	want := VNCStatus{Enabled: true, Port: 5901, State: "running", ServiceName: "cove"}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got VNCStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Port != want.Port || got.State != want.State || got.ServiceName != want.ServiceName || !got.Enabled {
		t.Fatalf("round trip = %#v", got)
	}
}
