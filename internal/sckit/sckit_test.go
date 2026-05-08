package sckit

import "testing"

func TestMacOSVersionAtLeast(t *testing.T) {
	tests := []struct {
		got        string
		wantMajor  int
		wantMinor  int
		want       bool
	}{
		{"14.0", 14, 0, true},
		{"14.5", 14, 0, true},
		{"15.1", 14, 0, true},
		{"13.7", 14, 0, false},
		{"14", 14, 0, true},
		{"14.0.1", 14, 0, true},
		{"14.0", 14, 1, false},
		{"15.0", 14, 99, true},
		{"", 14, 0, false},
		{"sequoia", 14, 0, false},
	}
	for _, tt := range tests {
		got := macOSVersionAtLeast(tt.got, tt.wantMajor, tt.wantMinor)
		if got != tt.want {
			t.Errorf("macOSVersionAtLeast(%q, %d, %d) = %v, want %v", tt.got, tt.wantMajor, tt.wantMinor, got, tt.want)
		}
	}
}

func TestParseDottedVersion(t *testing.T) {
	tests := []struct {
		in        string
		wantMajor int
		wantMinor int
	}{
		{"14.5", 14, 5},
		{"14.0.1", 14, 0},
		{"15", 15, 0},
		{"", 0, 0},
		{"abc", 0, 0},
		{"14.x", 14, 0},
	}
	for _, tt := range tests {
		gotMajor, gotMinor := parseDottedVersion(tt.in)
		if gotMajor != tt.wantMajor || gotMinor != tt.wantMinor {
			t.Errorf("parseDottedVersion(%q) = (%d,%d), want (%d,%d)", tt.in, gotMajor, gotMinor, tt.wantMajor, tt.wantMinor)
		}
	}
}

func TestDetectReturnsProbe(t *testing.T) {
	p := Detect()
	_ = p.SCKitAvailable
	_ = p.ScreenRecordingAuthorized
	_ = p.MacOSVersion
}
