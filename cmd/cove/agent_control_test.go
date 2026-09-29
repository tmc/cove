package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	agentstate "github.com/tmc/cove/internal/agent"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestAgentMountVolumesResponseSuccess(t *testing.T) {
	resp := agentMountVolumesResponse([]map[string]interface{}{
		{"tag": "src", "mountPoint": "/Volumes/src", "mounted": true},
	})
	if !resp.Success {
		t.Fatalf("resp.Success = false, want true")
	}
	if resp.Error != "" {
		t.Fatalf("resp.Error = %q, want empty", resp.Error)
	}
}

func TestAgentMountVolumesResponseFailure(t *testing.T) {
	resp := agentMountVolumesResponse([]map[string]interface{}{
		{"tag": "src", "mountPoint": "/Volumes/src", "error": "mount failed"},
	})
	if resp.Success {
		t.Fatalf("resp.Success = true, want false")
	}
}

func TestAgentSSHDArgsByGuestOS(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		linuxMode bool
		want      []string
	}{
		{
			name:   "macos status",
			action: "status",
			want:   []string{"systemsetup", "-getremotelogin"},
		},
		{
			name:      "linux status",
			action:    "status",
			linuxMode: true,
			want:      []string{"systemctl", "show", "-p", "ActiveState", "--value", "ssh"},
		},
		{
			name:      "linux on",
			action:    "on",
			linuxMode: true,
			want:      []string{"systemctl", "enable", "--now", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux off",
			action:    "off",
			linuxMode: true,
			want:      []string{"systemctl", "disable", "--now", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux start",
			action:    "start",
			linuxMode: true,
			want:      []string{"systemctl", "start", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux stop",
			action:    "stop",
			linuxMode: true,
			want:      []string{"systemctl", "stop", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux enable",
			action:    "enable",
			linuxMode: true,
			want:      []string{"systemctl", "enable", "--now", "ssh.service", "ssh.socket"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := agentSSHDArgs(tt.action, tt.linuxMode)
			if err != nil {
				t.Fatalf("agentSSHDArgs() error = %v", err)
			}
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("agentSSHDArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentSSHDLinuxOffAndStopMentionSocket(t *testing.T) {
	for _, action := range []string{"off", "stop"} {
		t.Run(action, func(t *testing.T) {
			got, err := agentSSHDArgs(action, true)
			if err != nil {
				t.Fatalf("agentSSHDArgs(%q) error = %v", action, err)
			}
			joined := strings.Join(got, " ")
			for _, want := range []string{"ssh.service", "ssh.socket"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("agentSSHDArgs(%q) = %q, missing %q", action, got, want)
				}
			}
		})
	}
}

func TestAgentSSHDArgsRejectsUnknown(t *testing.T) {
	if _, err := agentSSHDArgs("bogus", true); err == nil {
		t.Fatal("agentSSHDArgs() error = nil, want error")
	}
}

func TestGuestDir(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/etc/hosts", "/etc"},
		{"/var/log/system.log", "/var/log"},
		{"/file", "/"},
		{"file", "."},
		{"", "."},
	}
	for _, tt := range tests {
		if got := guestDir(tt.in); got != tt.want {
			t.Errorf("guestDir(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResponseTextValidUTF8(t *testing.T) {
	if got := responseText([]byte("ok")); got != "ok" {
		t.Fatalf("responseText(valid) = %q, want ok", got)
	}
	got := responseText([]byte{0xff, 'o', 'k'})
	if !utf8.ValidString(got) {
		t.Fatalf("responseText returned invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Fatalf("responseText(%v) = %q, want replacement plus payload", []byte{0xff, 'o', 'k'}, got)
	}
}

func TestIsUserPathOrHome(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"", false},
		{"/Users/me/Desktop/a.txt", true},
		{"/Users/me/Downloads", true},
		{"/Users/me/.bashrc", true},
		{"/Users/Shared/foo", false},
		{"/Users/Shared", false},
		{"~/Desktop/a.txt", true},
		{"~/Documents", true},
		{"/Volumes/share/foo", true},
		{"/Volumes/Macintosh HD/etc/hosts", false},
		{"/var/log/system.log", false},
		{"/tmp/test.txt", false},
		{"/home/ubuntu/file.txt", true},
		{"/home/ubuntu", true},
		{"/etc/hosts", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isUserPathOrHome(tt.path); got != tt.want {
				t.Errorf("isUserPathOrHome(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestTargetUserForPath(t *testing.T) {
	s := &ControlServer{}
	tests := []struct {
		path string
		want string
	}{
		{"/Users/alice/Desktop/file.txt", "alice"},
		{"/Users/bob/Downloads", "bob"},
		{"/Users/carol/.zshrc", "carol"},
		{"/Users/Shared/notes.txt", ""},
		{"/home/ubuntu/test.txt", "ubuntu"},
		{"/tmp/scratch.txt", ""},
		{"/var/log/install.log", ""},
		{"/etc/hosts", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := s.targetUserForPath(tt.path); got != tt.want {
				t.Errorf("targetUserForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestHandleAgentCopyInputValidation(t *testing.T) {
	s := &ControlServer{}
	tests := []struct {
		name    string
		cmd     *controlpb.AgentCopyCommand
		wantErr string
	}{
		{
			name:    "empty command",
			cmd:     &controlpb.AgentCopyCommand{},
			wantErr: "host_path and guest_path required",
		},
		{
			name:    "missing guest path",
			cmd:     &controlpb.AgentCopyCommand{HostPath: "/tmp/foo"},
			wantErr: "host_path and guest_path required",
		},
		{
			name:    "missing host path",
			cmd:     &controlpb.AgentCopyCommand{GuestPath: "/tmp/foo"},
			wantErr: "host_path and guest_path required",
		},
		{
			name: "nonexistent host path to guest",
			cmd: &controlpb.AgentCopyCommand{
				HostPath:  "/nonexistent-host-file-12345",
				GuestPath: "/tmp/file.txt",
				ToGuest:   true,
			},
			wantErr: "stat /nonexistent-host-file-12345",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := s.handleAgentCopy(tt.cmd)
			if resp.Success {
				t.Fatalf("handleAgentCopy() success = true, want failure with %q", tt.wantErr)
			}
			if !strings.Contains(resp.Error, tt.wantErr) {
				t.Fatalf("handleAgentCopy() error = %q, want error containing %q", resp.Error, tt.wantErr)
			}
		})
	}
}

func TestAgentCopyRouting(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		linuxGuest bool
		want       agentstate.Route
	}{
		{name: "desktop routes to user", path: "/Users/alice/Desktop/test.txt", want: agentstate.RouteUser},
		{name: "downloads routes to user", path: "/Users/alice/Downloads", want: agentstate.RouteUser},
		{name: "documents routes to user", path: "/Users/alice/Documents/doc.pdf", want: agentstate.RouteUser},
		{name: "tilde desktop routes to user", path: "~/Desktop/test.txt", want: agentstate.RouteUser},
		{name: "virtiofs volume routes to user", path: "/Volumes/My Shared Files/test.txt", want: agentstate.RouteUser},
		{name: "tmp routes to daemon", path: "/tmp/scratch.txt", want: agentstate.RouteDaemon},
		{name: "system var routes to daemon", path: "/var/log/install.log", want: agentstate.RouteDaemon},
		{name: "system volume routes to daemon", path: "/Volumes/Macintosh HD/etc/hosts", want: agentstate.RouteDaemon},
		{name: "linux guest forces daemon", path: "/Users/alice/Desktop/test.txt", linuxGuest: true, want: agentstate.RouteDaemon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentstate.RouteFor("cp", tt.path, tt.linuxGuest); got != tt.want {
				t.Errorf("RouteFor(cp, %q, linux=%v) = %v, want %v", tt.path, tt.linuxGuest, got, tt.want)
			}
		})
	}
}

