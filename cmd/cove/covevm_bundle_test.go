//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoveVMBundlePathArg(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dev.covevm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	got, ok := coveVMBundlePathArg([]string{dir})
	if !ok {
		t.Fatal("coveVMBundlePathArg() ok = false, want true")
	}
	if got != dir {
		t.Fatalf("coveVMBundlePathArg() = %q, want %q", got, dir)
	}

	if _, ok := coveVMBundlePathArg([]string{dir, "extra"}); ok {
		t.Fatal("coveVMBundlePathArg() ok = true for multiple args, want false")
	}
	if _, ok := coveVMBundlePathArg([]string{filepath.Join(t.TempDir(), "dev")}); ok {
		t.Fatal("coveVMBundlePathArg() ok = true for non-covevm path, want false")
	}
}

func TestCoveVMNameForPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "bundle", path: "/tmp/Dev.covevm", want: "Dev"},
		{name: "plain", path: "/tmp/Dev", want: "Dev"},
		{name: "case folded extension", path: "/tmp/Dev.COVEVM", want: "Dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := coveVMNameForPath(tt.path); got != tt.want {
				t.Fatalf("coveVMNameForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestInstallCoveVMDocumentTypes(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Cove.app")
	contents := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(contents, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	plist := filepath.Join(contents, "Info.plist")
	if err := os.WriteFile(plist, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>cove</string>
</dict>
</plist>
`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := installCoveVMDocumentTypes(bundle); err != nil {
		t.Fatalf("installCoveVMDocumentTypes() error = %v", err)
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"<key>CFBundleDocumentTypes</key>",
		"<key>UTExportedTypeDeclarations</key>",
		"<string>com.tmc.cove.vm</string>",
		"<key>LSTypeIsPackage</key>",
		"<true/>",
		"<string>covevm</string>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Info.plist missing %q:\n%s", want, got)
		}
	}
}

func TestWantsMacgoRuntimeForCoveVMBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dev.covevm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if !wantsMacgoRuntime([]string{dir}, false, false, "") {
		t.Fatal("wantsMacgoRuntime(covevm) = false, want true")
	}
}
