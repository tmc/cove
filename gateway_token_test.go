package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMasterTokenFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.token")

	tok1, err := LoadOrCreateMasterToken(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	assertTokenFormat(t, tok1)

	tok2, err := LoadOrCreateMasterToken(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if tok1 != tok2 {
		t.Errorf("token changed on re-read: %q != %q", tok1, tok2)
	}
}

func TestMasterTokenFileRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.token")

	tok1, err := LoadOrCreateMasterToken(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	tok2, err := RotateMasterToken(path)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	assertTokenFormat(t, tok2)
	if tok1 == tok2 {
		t.Error("rotation did not change token")
	}

	// Confirm new token persisted.
	tok3, err := LoadOrCreateMasterToken(path)
	if err != nil {
		t.Fatalf("load after rotate: %v", err)
	}
	if tok2 != tok3 {
		t.Errorf("token after rotate mismatch: %q != %q", tok2, tok3)
	}
}

func TestMasterTokenWiderPermsWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.token")

	// Create file with too-wide permissions.
	if err := os.WriteFile(path, []byte("aabbccdd\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stderr, restore := captureStderr(t)
	defer restore()

	_, err := LoadOrCreateMasterToken(path)
	restore()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	warn := stderr.String()
	if !strings.Contains(warn, "permissions") {
		t.Errorf("expected permissions warning on stderr; got: %q", warn)
	}
}

func TestMasterTokenFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fmt.token")

	tok, err := LoadOrCreateMasterToken(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertTokenFormat(t, tok)
}

// TestKeychainRoundTrip exercises KeychainGetGenericPassword / KeychainSetGenericPassword /
// KeychainDeleteGenericPassword against the real macOS keychain. Skipped by default;
// set COVE_TEST_KEYCHAIN=1 to run (requires a GUI session with keychain access).
func TestKeychainRoundTrip(t *testing.T) {
	if os.Getenv("COVE_TEST_KEYCHAIN") != "1" {
		t.Skip("set COVE_TEST_KEYCHAIN=1 to run keychain integration tests")
	}

	svc := fmt.Sprintf("com.tmc.cove.gateway.test.%d", os.Getpid())
	acc := "test-account"
	want := []byte("hello-keychain-data")

	t.Cleanup(func() {
		_ = KeychainDeleteGenericPassword(svc, acc)
	})

	// Should not exist yet.
	_, err := KeychainGetGenericPassword(svc, acc)
	if err == nil {
		t.Fatal("expected not-found before set")
	}

	// Store.
	if err := KeychainSetGenericPassword(svc, acc, "test label", "test desc", want); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Retrieve.
	got, err := KeychainGetGenericPassword(svc, acc)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}

	// Update.
	want2 := []byte("updated-value")
	if err := KeychainSetGenericPassword(svc, acc, "test label", "test desc", want2); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, err := KeychainGetGenericPassword(svc, acc)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if string(got2) != string(want2) {
		t.Errorf("after update got %q, want %q", got2, want2)
	}

	// Delete.
	if err := KeychainDeleteGenericPassword(svc, acc); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = KeychainGetGenericPassword(svc, acc)
	if err == nil {
		t.Fatal("expected not-found after delete")
	}
}

// TestMasterTokenKeychain tests LoadOrCreateMasterToken keychain path end-to-end.
// Skipped by default; set COVE_TEST_KEYCHAIN=1 to run.
func TestMasterTokenKeychain(t *testing.T) {
	if os.Getenv("COVE_TEST_KEYCHAIN") != "1" {
		t.Skip("set COVE_TEST_KEYCHAIN=1 to run keychain integration tests")
	}

	// Clean up any existing item from a prior run.
	_ = KeychainDeleteGenericPassword(keychainService, keychainAccount)
	t.Cleanup(func() {
		_ = KeychainDeleteGenericPassword(keychainService, keychainAccount)
	})

	tok1, err := LoadOrCreateMasterToken("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	assertTokenFormat(t, tok1)

	tok2, err := LoadOrCreateMasterToken("")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if tok1 != tok2 {
		t.Errorf("token changed on re-read: %q != %q", tok1, tok2)
	}
}

func TestWriteTokenFileCreatesNestedDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", "tok")

	if err := writeTokenFile(path, "deadbeef"); err != nil {
		t.Fatalf("writeTokenFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(data); got != "deadbeef\n" {
		t.Errorf("contents = %q, want %q", got, "deadbeef\n")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("token file perms = %04o, want 0600", perm)
	}
}

func TestLoadOrCreateFileTokenStatErrorIsReturned(t *testing.T) {
	dir := t.TempDir()
	// A path under a non-directory parent triggers a non-IsNotExist stat error.
	parent := filepath.Join(dir, "notadir")
	if err := os.WriteFile(parent, []byte("x"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := filepath.Join(parent, "tok")

	_, err := loadOrCreateFileToken(path)
	if err == nil {
		t.Fatal("loadOrCreateFileToken: expected error for path under non-directory parent")
	}
}

// assertTokenFormat checks that tok is exactly 64 lowercase hex chars.
func assertTokenFormat(t *testing.T, tok string) {
	t.Helper()
	if len(tok) != 64 {
		t.Errorf("token length %d, want 64: %q", len(tok), tok)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(tok) {
		t.Errorf("token not 64 hex chars: %q", tok)
	}
}

// captureStderr swaps os.Stderr with a pipe and returns a buffer and a restore func.
func captureStderr(t *testing.T) (*strings.Builder, func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w

	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 4096)
		for {
			n, err := r.Read(b)
			if n > 0 {
				buf.Write(b[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	restore := func() {
		os.Stderr = old
		w.Close()
		<-done
		r.Close()
	}
	return &buf, restore
}
