package main

import (
	"strings"
	"testing"
	"unicode/utf8"
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
