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
			want:      []string{"sh", "-lc", "systemctl status ssh --no-pager || systemctl status sshd --no-pager"},
		},
		{
			name:      "linux on",
			action:    "on",
			linuxMode: true,
			want:      []string{"sh", "-lc", "systemctl enable --now ssh || systemctl enable --now sshd"},
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

func TestAgentSSHDArgsRejectsUnknown(t *testing.T) {
	if _, err := agentSSHDArgs("bogus", true); err == nil {
		t.Fatal("agentSSHDArgs() error = nil, want error")
	}
}

func TestParseConsoleOwnerOutput(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		wantUser string
		wantUID  int
		wantErr  bool
	}{
		{name: "user", stdout: "user 501\n", wantUser: "user", wantUID: 501},
		{name: "root", stdout: "root 0\n", wantErr: true},
		{name: "bad uid", stdout: "user nope\n", wantErr: true},
		{name: "bad format", stdout: "user\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUser, gotUID, err := parseConsoleOwnerOutput(tt.stdout)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseConsoleOwnerOutput(%q): got nil error", tt.stdout)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConsoleOwnerOutput(%q): %v", tt.stdout, err)
			}
			if gotUser != tt.wantUser || gotUID != tt.wantUID {
				t.Fatalf("parseConsoleOwnerOutput(%q): got (%q, %d), want (%q, %d)", tt.stdout, gotUser, gotUID, tt.wantUser, tt.wantUID)
			}
		})
	}
}

func TestResponseTextValidUTF8(t *testing.T) {
	got := responseText([]byte{0xff, 'o', 'k'})
	if !utf8.ValidString(got) {
		t.Fatalf("responseText returned invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Fatalf("responseText(%v) = %q, want replacement plus payload", []byte{0xff, 'o', 'k'}, got)
	}
}
