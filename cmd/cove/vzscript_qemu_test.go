package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQEMUKeySpec(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{in: "return", want: "ret"},
		{in: "shift+f10", want: "shift-f10"},
		{in: "alt+c", want: "alt-c"},
		{in: "cmd+v", want: "meta_l-v"},
		{in: "backslash", want: "backslash"},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got, err := qemuKeySpec(tt.in)
			if err != nil {
				t.Fatalf("qemuKeySpec(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("qemuKeySpec(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
	if _, err := qemuKeySpec("bogus"); err == nil {
		t.Fatalf("qemuKeySpec(bogus) succeeded")
	}
}

func TestQEMUKeyForRune(t *testing.T) {
	for _, tt := range []struct {
		in   rune
		want string
	}{
		{in: 'A', want: "shift-a"},
		{in: 'z', want: "z"},
		{in: '\\', want: "backslash"},
		{in: '!', want: "shift-1"},
		{in: '\n', want: "ret"},
	} {
		t.Run(string(tt.in), func(t *testing.T) {
			got, err := qemuKeyForRune(tt.in)
			if err != nil {
				t.Fatalf("qemuKeyForRune(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("qemuKeyForRune(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecodePPM(t *testing.T) {
	img, err := decodePPM(strings.NewReader("P6\n# comment\n2 1\n255\n\xff\x00\x00\x00\xff\x00"))
	if err != nil {
		t.Fatalf("decodePPM: %v", err)
	}
	if got := img.Bounds().Dx(); got != 2 {
		t.Fatalf("width = %d, want 2", got)
	}
	if got := img.Bounds().Dy(); got != 1 {
		t.Fatalf("height = %d, want 1", got)
	}
	r, g, b, _ := img.At(0, 0).RGBA()
	if r != 0xffff || g != 0 || b != 0 {
		t.Fatalf("pixel 0 = %x %x %x, want red", r, g, b)
	}
}

func TestQEMUAgentAddressForVMDir(t *testing.T) {
	dir := t.TempDir()
	qemuDir := filepath.Join(dir, "qemu")
	if err := os.Mkdir(qemuDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "metadata.json"), []byte(`{"agentHostAddress":"127.0.0.1","agentHostPort":32102}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := qemuAgentAddressForVMDir(dir); got != "127.0.0.1:32102" {
		t.Fatalf("qemuAgentAddressForVMDir = %q, want 127.0.0.1:32102", got)
	}
}

func TestWindowsSetupActionForText(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		want string
	}{
		{name: "language", text: "Select language settings", want: "accept language settings"},
		{name: "old language", text: "Enter your language and other preferences and click Next to continue.", want: "apply windows 11 setup bypass"},
		{name: "uefi shell", text: "UEFI Interactive Shell v2.2\nShell>", want: "start windows installer from uefi shell"},
		{name: "link error", text: "Unable to open link. Please visit https://aka.ms/SetupFaq", want: "dismiss setup link message"},
		{name: "product key", text: "Product key\nEnter Product key\nI don't have a product key", want: "skip product key"},
		{name: "product key footer", text: "Activate Windows\nI don't have a product key\nCollecting information\nInstalling Windows", want: "skip product key"},
		{name: "install type", text: "Which type of installation do you want?\nCustom: Install Windows only", want: "select custom install"},
		{name: "compatibility", text: "Compatibility report\nThe upgrade option isn't available", want: "close compatibility report"},
		{name: "install progress", text: "Installing Windows\nGetting files ready for installation", want: "wait for install progress"},
		{name: "new install progress", text: "Installing Windows 11\n14% complete", want: "wait for install progress"},
		{name: "select image", text: "Select Image\nPlease select the image you want to install", want: "apply windows 11 setup bypass"},
		{name: "oobe region", text: "Is this the right country or region?", want: "accept oobe region"},
		{name: "oobe keyboard", text: "Is this the right keyboard layout or input method?", want: "accept oobe keyboard"},
		{name: "oobe second keyboard", text: "Want to add a second keyboard layout?", want: "skip second keyboard"},
		{name: "offline", text: "Let's connect you to a network", want: "open oobe command prompt"},
		{name: "first logon wait", text: "Just a moment...\nPlease keep your PC on and plugged in.\nDon't turn off your PC", want: "wait for install progress"},
		{name: "cmd", text: `Administrator: C:\Windows\system32\cmd.exe C:\Windows\System32>`, want: "run oobe network bypass"},
		{name: "start search desktop", text: "Search for apps, settings, and documents\nTop apps\nSettings\nCalculator", want: "desktop reached"},
		{name: "desktop", text: "Recycle Bin", want: "desktop reached"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := windowsSetupActionForText(tt.text)
			if got == nil {
				t.Fatalf("windowsSetupActionForText(%q) = nil", tt.text)
			}
			if got.name != tt.want {
				t.Fatalf("action = %q, want %q", got.name, tt.want)
			}
		})
	}
}

func TestWindowsSetupActionForTextState(t *testing.T) {
	got := windowsSetupActionForTextState("Select Image\nPlease select the image you want to install", windowsSetupState{labConfigApplied: true})
	if got == nil {
		t.Fatalf("select image with labConfigApplied = nil")
	}
	if got.name != "select default edition" {
		t.Fatalf("action = %q, want select default edition", got.name)
	}

	got = windowsSetupActionForTextState("Press any key to boot from CD or DVD", windowsSetupState{})
	if got == nil {
		t.Fatalf("initial cd prompt = nil")
	}
	if got.name != "boot from windows installer" {
		t.Fatalf("action = %q, want boot from windows installer", got.name)
	}

	got = windowsSetupActionForTextState("Press any key to boot from CD or DVD", windowsSetupState{installStarted: true})
	if got == nil {
		t.Fatalf("post-install cd prompt = nil")
	}
	if got.name != "ignore cd boot prompt after reboot" {
		t.Fatalf("action = %q, want ignore cd boot prompt after reboot", got.name)
	}

	got = windowsSetupActionForTextState("This PC doesn't currently meet Windows 11 system requirements", windowsSetupState{})
	if got == nil {
		t.Fatalf("requirements page = nil")
	}
	if got.err == "" {
		t.Fatalf("requirements page action has no error")
	}
}
