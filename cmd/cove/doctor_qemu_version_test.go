package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseQEMUVersion(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    qemuVersion
		wantErr bool
	}{
		{name: "release", line: "QEMU emulator version 9.1.0", want: qemuVersion{9, 1, 0}},
		{name: "packaged", line: "QEMU emulator version 8.2.2 (v8.2.2)", want: qemuVersion{8, 2, 2}},
		{name: "release candidate", line: "QEMU emulator version 8.0.0-rc1", want: qemuVersion{8, 0, 0}},
		{name: "dirty build", line: "QEMU emulator version 6.2.0-dirty", want: qemuVersion{6, 2, 0}},
		{name: "two components", line: "QEMU emulator version 5.2", want: qemuVersion{5, 2, 0}},
		{name: "img", line: "qemu-img version 9.0.1", want: qemuVersion{9, 0, 1}},
		{name: "empty", line: "", wantErr: true},
		{name: "no version word", line: "QEMU emulator 9.1.0", wantErr: true},
		{name: "trailing version word", line: "QEMU emulator version", wantErr: true},
		{name: "not a number", line: "QEMU emulator version unknown", wantErr: true},
		{name: "too many components", line: "QEMU emulator version 1.2.3.4", wantErr: true},
		{name: "signed component", line: "QEMU emulator version 9.+1.0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQEMUVersion(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseQEMUVersion(%q) = %v, want error", tt.line, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseQEMUVersion(%q): %v", tt.line, err)
			}
			if got != tt.want {
				t.Fatalf("parseQEMUVersion(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestQEMUVersionLess(t *testing.T) {
	tests := []struct {
		name string
		v, o qemuVersion
		want bool
	}{
		{name: "major", v: qemuVersion{5, 9, 9}, o: qemuVersion{6, 0, 0}, want: true},
		{name: "minor", v: qemuVersion{6, 0, 0}, o: qemuVersion{6, 1, 0}, want: true},
		{name: "patch", v: qemuVersion{6, 1, 0}, o: qemuVersion{6, 1, 1}, want: true},
		{name: "equal", v: qemuVersion{6, 0, 0}, o: qemuVersion{6, 0, 0}},
		{name: "newer", v: qemuVersion{9, 1, 0}, o: qemuVersion{6, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.less(tt.o); got != tt.want {
				t.Fatalf("%v.less(%v) = %v, want %v", tt.v, tt.o, got, tt.want)
			}
		})
	}
}

func TestQEMUDoctorVersionStatus(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantStatus string
		wantSubstr string
	}{
		{name: "new enough", line: "QEMU emulator version 9.1.0", wantStatus: "pass", wantSubstr: "found QEMU 9.1.0, need 6.0.0 or newer"},
		{name: "exactly minimum", line: "QEMU emulator version 6.0.0", wantStatus: "pass"},
		{name: "too old", line: "QEMU emulator version 5.2.0", wantStatus: "fail", wantSubstr: "brew install qemu"},
		{name: "too old reports both versions", line: "QEMU emulator version 5.2.0", wantStatus: "fail", wantSubstr: "found QEMU 5.2.0, need 6.0.0 or newer"},
		{name: "unreadable", line: "no version here", wantStatus: "warn", wantSubstr: "brew install qemu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := qemuDoctorVersionStatus(tt.line)
			if got.Name != "qemu-version" {
				t.Fatalf("check name = %q, want qemu-version", got.Name)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (%s)", got.Status, tt.wantStatus, got.Message)
			}
			if tt.wantSubstr != "" && !strings.Contains(got.Message, tt.wantSubstr) {
				t.Fatalf("message = %q, want it to contain %q", got.Message, tt.wantSubstr)
			}
		})
	}
}

func TestQEMUDoctorAquaSessionStatus(t *testing.T) {
	tests := []struct {
		name       string
		session    string
		err        error
		wantStatus string
		wantSubstr string
	}{
		{name: "console", session: "Aqua", wantStatus: "pass"},
		{name: "case insensitive", session: "aqua", wantStatus: "pass"},
		{name: "ssh", session: "Background", wantStatus: "warn", wantSubstr: "launchctl asuser"},
		{name: "empty", session: "", wantStatus: "warn", wantSubstr: "unknown"},
		{name: "launchctl failed", err: errors.New("boom"), wantStatus: "warn", wantSubstr: "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := qemuDoctorAquaSessionStatus(tt.session, tt.err)
			if got.Name != "aqua-session" {
				t.Fatalf("check name = %q, want aqua-session", got.Name)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (%s)", got.Status, tt.wantStatus, got.Message)
			}
			if tt.wantSubstr != "" && !strings.Contains(got.Message, tt.wantSubstr) {
				t.Fatalf("message = %q, want it to contain %q", got.Message, tt.wantSubstr)
			}
		})
	}
}
