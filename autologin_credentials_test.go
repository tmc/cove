package main

import (
	"os"
	"path/filepath"
	"testing"

	pw "github.com/tmc/vz-macos/internal/password"
)

func TestReadLoginScreenCredentials(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "private", "etc"), 0755); err != nil {
		t.Fatalf("MkdirAll kcpassword dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Library", "Preferences"), 0755); err != nil {
		t.Fatalf("MkdirAll prefs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "private", "etc", "kcpassword"), pw.EncodeKC("secret123"), 0600); err != nil {
		t.Fatalf("WriteFile kcpassword: %v", err)
	}
	data, err := pw.EncodeLoginWindowPlist(pw.CreateLoginWindowPlist("testuser"))
	if err != nil {
		t.Fatalf("EncodeLoginWindowPlist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Library", "Preferences", "com.apple.loginwindow.plist"), data, 0644); err != nil {
		t.Fatalf("WriteFile loginwindow plist: %v", err)
	}

	creds, err := readLoginScreenCredentials(root)
	if err != nil {
		t.Fatalf("readLoginScreenCredentials: %v", err)
	}
	if creds.Username != "testuser" {
		t.Fatalf("Username = %q, want %q", creds.Username, "testuser")
	}
	if creds.Password != "secret123" {
		t.Fatalf("Password = %q, want %q", creds.Password, "secret123")
	}
}

func TestReadLoginScreenCredentialsMissingFiles(t *testing.T) {
	creds, err := readLoginScreenCredentials(t.TempDir())
	if err != nil {
		t.Fatalf("readLoginScreenCredentials: %v", err)
	}
	if creds.Valid() {
		t.Fatalf("readLoginScreenCredentials returned unexpected credentials: %+v", creds)
	}
}

func TestLoginScreenCredentialsCacheRoundTrip(t *testing.T) {
	vmDir := t.TempDir()
	want := loginScreenCredentials{Username: "testuser", Password: "secret123"}
	if err := writeLoginScreenCredentialsCache(vmDir, want); err != nil {
		t.Fatalf("writeLoginScreenCredentialsCache: %v", err)
	}
	got, err := readLoginScreenCredentialsCache(vmDir)
	if err != nil {
		t.Fatalf("readLoginScreenCredentialsCache: %v", err)
	}
	if got != want {
		t.Fatalf("cache = %+v, want %+v", got, want)
	}
	info, err := os.Stat(filepath.Join(vmDir, loginScreenCredentialsFile))
	if err != nil {
		t.Fatalf("Stat cache file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("cache mode = %o, want 600", mode)
	}
}

func TestWriteLoginScreenCredentialsCacheSkipsInvalid(t *testing.T) {
	vmDir := t.TempDir()
	cases := []loginScreenCredentials{
		{},
		{Username: "u"},
		{Password: "p"},
	}
	for _, c := range cases {
		if err := writeLoginScreenCredentialsCache(vmDir, c); err != nil {
			t.Fatalf("writeLoginScreenCredentialsCache(%+v): %v", c, err)
		}
		if _, err := os.Stat(filepath.Join(vmDir, loginScreenCredentialsFile)); !os.IsNotExist(err) {
			t.Fatalf("cache file written for invalid creds %+v: err=%v", c, err)
		}
	}
}

func TestReadLoginScreenCredentialsCacheCorrupt(t *testing.T) {
	vmDir := t.TempDir()
	path := filepath.Join(vmDir, loginScreenCredentialsFile)

	// Valid JSON but invalid creds (empty fields) -> empty, no error.
	if err := os.WriteFile(path, []byte(`{"Username":"","Password":""}`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := readLoginScreenCredentialsCache(vmDir)
	if err != nil {
		t.Fatalf("readLoginScreenCredentialsCache (empty fields): %v", err)
	}
	if got.Valid() {
		t.Fatalf("got = %+v, want empty", got)
	}

	// Malformed JSON -> error.
	if err := os.WriteFile(path, []byte("not-json"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := readLoginScreenCredentialsCache(vmDir); err == nil {
		t.Fatalf("readLoginScreenCredentialsCache: want error for malformed JSON")
	}
}

func TestLoadBootLoginScreenCredentialsDoesNotMountDisk(t *testing.T) {
	creds, err := loadBootLoginScreenCredentials(t.TempDir(), filepath.Join(t.TempDir(), "missing.img"))
	if err != nil {
		t.Fatalf("loadBootLoginScreenCredentials: %v", err)
	}
	if creds.Valid() {
		t.Fatalf("credentials = %+v, want empty", creds)
	}
}
