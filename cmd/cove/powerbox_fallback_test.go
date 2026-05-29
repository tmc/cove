//go:build darwin

package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPowerboxFallbackRetriesOnSuccess(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "bookmarks.json")
	grantPath := filepath.Join(t.TempDir(), "vm-root")
	oldPrompt := powerboxPromptDirectory
	t.Cleanup(func() { powerboxPromptDirectory = oldPrompt })
	promptCalls := 0
	powerboxPromptDirectory = func(title, message string) (powerboxDirectoryGrant, error) {
		promptCalls++
		if title == "" || message == "" {
			t.Fatalf("prompt title/message should be set, got %q %q", title, message)
		}
		return powerboxDirectoryGrant{
			Path:     grantPath,
			Bookmark: []byte("bookmark-bytes"),
		}, nil
	}

	actionCalls := 0
	err := withPowerboxFallback(func() error {
		actionCalls++
		if actionCalls == 1 {
			return powerboxGrantRequired("resolve VM", "vm:test", storePath)
		}
		store, err := readSecurityBookmarkStore(storePath)
		if err != nil {
			t.Fatalf("readSecurityBookmarkStore: %v", err)
		}
		entry, ok := store.Entries["vm:test"]
		if !ok {
			t.Fatalf("stored bookmark missing vm:test")
		}
		if entry.Kind != "vm-root" || entry.Path != grantPath {
			t.Fatalf("stored bookmark = %+v", entry)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withPowerboxFallback: %v", err)
	}
	if actionCalls != 2 {
		t.Fatalf("action calls = %d, want 2", actionCalls)
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
}

func TestPowerboxFallbackFailsOnCancel(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "bookmarks.json")
	oldPrompt := powerboxPromptDirectory
	t.Cleanup(func() { powerboxPromptDirectory = oldPrompt })
	promptCalls := 0
	powerboxPromptDirectory = func(title, message string) (powerboxDirectoryGrant, error) {
		promptCalls++
		return powerboxDirectoryGrant{}, errPowerboxCanceled
	}

	actionCalls := 0
	err := withPowerboxFallback(func() error {
		actionCalls++
		return powerboxGrantRequired("resolve VM", "vm:test", storePath)
	})
	if !errors.Is(err, errPowerboxCanceled) {
		t.Fatalf("withPowerboxFallback error = %v, want errPowerboxCanceled", err)
	}
	if actionCalls != 1 {
		t.Fatalf("action calls = %d, want 1", actionCalls)
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
	store, err := readSecurityBookmarkStore(storePath)
	if err != nil {
		t.Fatalf("readSecurityBookmarkStore: %v", err)
	}
	if len(store.Entries) != 0 {
		t.Fatalf("store entries = %d, want 0", len(store.Entries))
	}
}

func TestPowerboxFileFallbackRetriesOnSuccess(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "bookmarks.json")
	grantPath := filepath.Join(t.TempDir(), "install.iso")
	oldPrompt := powerboxPromptFile
	t.Cleanup(func() { powerboxPromptFile = oldPrompt })
	promptCalls := 0
	powerboxPromptFile = func(title, message string, extensions []string) (powerboxFileGrant, error) {
		promptCalls++
		if title == "" || message == "" {
			t.Fatalf("prompt title/message should be set, got %q %q", title, message)
		}
		if len(extensions) != 1 || extensions[0] != "iso" {
			t.Fatalf("prompt extensions = %v, want [iso]", extensions)
		}
		return powerboxFileGrant{
			Path:     grantPath,
			Bookmark: []byte("bookmark-bytes"),
		}, nil
	}

	actionCalls := 0
	err := withPowerboxFallback(func() error {
		actionCalls++
		if actionCalls == 1 {
			return powerboxGrantRequiredKind("read media", "iso:/tmp/install.iso", "iso", storePath)
		}
		store, err := readSecurityBookmarkStore(storePath)
		if err != nil {
			t.Fatalf("readSecurityBookmarkStore: %v", err)
		}
		entry, ok := store.Entries["iso:/tmp/install.iso"]
		if !ok {
			t.Fatalf("stored bookmark missing iso:/tmp/install.iso")
		}
		if entry.Kind != "iso" || entry.Path != grantPath {
			t.Fatalf("stored bookmark = %+v", entry)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withPowerboxFallback: %v", err)
	}
	if actionCalls != 2 {
		t.Fatalf("action calls = %d, want 2", actionCalls)
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
}

func TestPowerboxFileFallbackFailsOnCancel(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "bookmarks.json")
	oldPrompt := powerboxPromptFile
	t.Cleanup(func() { powerboxPromptFile = oldPrompt })
	promptCalls := 0
	powerboxPromptFile = func(title, message string, extensions []string) (powerboxFileGrant, error) {
		promptCalls++
		return powerboxFileGrant{}, errPowerboxCanceled
	}

	actionCalls := 0
	err := withPowerboxFallback(func() error {
		actionCalls++
		return powerboxGrantRequiredKind("read media", "ipsw:/tmp/Restore.ipsw", "ipsw", storePath)
	})
	if !errors.Is(err, errPowerboxCanceled) {
		t.Fatalf("withPowerboxFallback error = %v, want errPowerboxCanceled", err)
	}
	if actionCalls != 1 {
		t.Fatalf("action calls = %d, want 1", actionCalls)
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
	store, err := readSecurityBookmarkStore(storePath)
	if err != nil {
		t.Fatalf("readSecurityBookmarkStore: %v", err)
	}
	if len(store.Entries) != 0 {
		t.Fatalf("store entries = %d, want 0", len(store.Entries))
	}
}
