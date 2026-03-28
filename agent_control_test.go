package main

import "testing"

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

func TestParseConsoleOwnerOutput(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		wantUser string
		wantUID int
		wantErr bool
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
