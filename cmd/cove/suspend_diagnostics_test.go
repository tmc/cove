package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tmc/apple/foundation"
)

func TestSuspendQuarantineFailurePreservesInputs(t *testing.T) {
	dir := t.TempDir()
	state, config := suspendStatePathForVM(dir), suspendConfigPathForVM(dir)
	for _, path := range []string{state, config} {
		if err := os.WriteFile(path, []byte("evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := errors.New("rename denied")
	err := quarantineSuspendState(dir, "test", func(string, string) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("quarantine error %v", err)
	}
	for _, path := range []string{state, config} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "evidence" {
			t.Fatalf("retained %s: %q %v", path, data, err)
		}
	}
}

func TestSuspendQuarantineFingerprintFailurePreservesEvidence(t *testing.T) {
	dir := t.TempDir()
	state, config := suspendStatePathForVM(dir), suspendConfigPathForVM(dir)
	for _, path := range []string{state, config} {
		if err := os.WriteFile(path, []byte("evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := errors.New("fingerprint rename denied")
	err := quarantineSuspendState(dir, "test", func(from, to string) error {
		if from == config {
			return want
		}
		return os.Rename(from, to)
	})
	if !errors.Is(err, want) {
		t.Fatalf("quarantine error %v", err)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original state: %v", err)
	}
	backups, err := filepath.Glob(state + ".broken-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("state backups %v: %v", backups, err)
	}
	for _, path := range []string{backups[0], config} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "evidence" {
			t.Fatalf("retained %s: %q %v", path, data, err)
		}
	}
	if _, err := os.Stat(backups[0] + ".config.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("quarantined fingerprint: %v", err)
	}
}

func TestNSErrorSnapshotUnderlyingFoundationError(t *testing.T) {
	key := foundation.NewStringWithString("NSUnderlyingError")
	underlying := foundation.NewErrorWithDomainCodeUserInfo("NSPOSIXErrorDomain", 13, nil)
	info := foundation.NewDictionaryWithObjectForKey(underlying, key)
	top := foundation.NewErrorWithDomainCodeUserInfo("VZErrorDomain", 12, info)
	got := snapshotNSError(&top)
	var snap nsErrorSnapshot
	if !errors.As(got, &snap) || snap.underlying == nil || snap.underlying.domain != "NSPOSIXErrorDomain" || snap.underlying.code != 13 {
		t.Fatalf("underlying snapshot %v", got)
	}
	malformed := foundation.NewDictionaryWithObjectForKey(key, key)
	top = foundation.NewErrorWithDomainCodeUserInfo("VZErrorDomain", 12, malformed)
	got = snapshotNSError(&top)
	if !errors.As(got, &snap) || snap.underlying != nil {
		t.Fatalf("malformed underlying snapshot %v", got)
	}
}

func TestNSErrorSnapshotTextUTF8(t *testing.T) {
	got := boundedNSErrorText(strings.Repeat("界", 400))
	if !utf8.ValidString(got) || len(got) > 1027 {
		t.Fatalf("invalid bounded text length %d", len(got))
	}
}

func TestNSErrorSnapshotChainBounds(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		calls := 0
		got := snapshotNSErrorChain(func() (uintptr, nsErrorSnapshot, bool) {
			calls++
			id := uintptr(calls)
			if cycle {
				id = 1
			}
			return id, nsErrorSnapshot{domain: "NSPOSIXErrorDomain", code: 13, description: strings.Repeat("x", 2048), reason: strings.Repeat("y", 2048)}, true
		})
		if calls > 4 || len(got.Error()) > 9<<10 {
			t.Fatalf("calls %d, bytes %d", calls, len(got.Error()))
		}
		if cycle && calls != 2 {
			t.Fatalf("cycle calls %d", calls)
		}
		if !cycle {
			if got.underlying == nil || got.underlying.code != 13 {
				t.Fatal("underlying code lost")
			}
			var underlying nsErrorSnapshot
			if !errors.As(got.Unwrap(), &underlying) {
				t.Fatal("underlying chain not unwrapable")
			}
		}
	}
}
