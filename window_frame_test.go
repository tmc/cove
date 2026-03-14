package main

import (
	"testing"

	"github.com/tmc/apple/corefoundation"
)

func TestWindowFrameAutosaveNameForVM(t *testing.T) {
	tests := []struct {
		name   string
		vmName string
		vmDir  string
		linux  bool
		want   string
	}{
		{
			name:   "mac default",
			vmName: "",
			vmDir:  "/Users/tmc/.vz/vms/default",
			linux:  false,
			want:   "com.github.tmc.vz-macos.window.macos.default",
		},
		{
			name:   "linux named vm",
			vmName: "macos-3",
			vmDir:  "/Users/tmc/.vz/vms/macos-3",
			linux:  true,
			want:   "com.github.tmc.vz-macos.window.linux.macos-3",
		},
		{
			name:   "sanitize token",
			vmName: "My VM/Dev",
			vmDir:  "/tmp/ignored",
			linux:  false,
			want:   "com.github.tmc.vz-macos.window.macos.my_vm_dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := windowFrameAutosaveNameForVM(tt.vmName, tt.vmDir, tt.linux)
			if got != tt.want {
				t.Fatalf("windowFrameAutosaveNameForVM() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeAutosaveToken(t *testing.T) {
	if got := sanitizeAutosaveToken("___"); got != "default" {
		t.Fatalf("sanitizeAutosaveToken(\"___\") = %q, want %q", got, "default")
	}
	if got := sanitizeAutosaveToken("VM Name!"); got != "vm_name" {
		t.Fatalf("sanitizeAutosaveToken(\"VM Name!\") = %q, want %q", got, "vm_name")
	}
}

func TestWindowDisplayPlacementPath(t *testing.T) {
	prevVMDir := vmDir
	vmDir = "/Users/tmc/.vz/vms/macos-3"
	t.Cleanup(func() { vmDir = prevVMDir })

	name := "com.github.tmc.vz-macos.window.macos.macos-3"
	got := windowDisplayPlacementPath(name)
	want := "/Users/tmc/.vz/vms/macos-3/window-display-com.github.tmc.vz-macos.window.macos.macos-3.json"
	if got != want {
		t.Fatalf("windowDisplayPlacementPath() = %q, want %q", got, want)
	}
}

func TestTranslateWindowFrameBetweenScreens(t *testing.T) {
	frame := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 240, Y: 180},
		Size:   corefoundation.CGSize{Width: 1024, Height: 768},
	}
	from := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: 1440, Height: 900},
	}
	to := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 1440, Y: 0},
		Size:   corefoundation.CGSize{Width: 2560, Height: 1440},
	}
	got := translateWindowFrameBetweenScreens(frame, from, to)
	if got.Origin.X != 1680 || got.Origin.Y != 180 {
		t.Fatalf("translateWindowFrameBetweenScreens() origin = (%v,%v), want (%v,%v)",
			got.Origin.X, got.Origin.Y, 1680, 180)
	}
	if got.Size != frame.Size {
		t.Fatalf("translateWindowFrameBetweenScreens() size changed: got %+v, want %+v", got.Size, frame.Size)
	}
}
