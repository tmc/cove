//go:build darwin

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type securityBookmarkStore struct {
	Version int                              `json:"version"`
	Entries map[string]securityBookmarkEntry `json:"entries"`
}

type securityBookmarkEntry struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Bookmark string `json:"bookmark"`
	Updated  string `json:"updated"`
}

type securityBookmarkStoreReport struct {
	Action string                        `json:"action"`
	Store  string                        `json:"store"`
	Key    string                        `json:"key"`
	Entry  securityBookmarkEntryReport   `json:"entry"`
	Proof  *securityScopedBookmarkReport `json:"proof,omitempty"`
}

type securityBookmarkEntryReport struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	BookmarkSize int    `json:"bookmark_bytes"`
	Updated      string `json:"updated"`
}

const securityBookmarkStoreEnv = "COVE_BOOKMARK_STORE"

type securityBookmarkAccess struct {
	Entry        securityBookmarkEntry
	Path         string
	Stale        bool
	Stop         func()
	BookmarkSize int
}

func defaultSecurityBookmarkStorePath() (string, error) {
	if path := os.Getenv(securityBookmarkStoreEnv); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "cove", "security-scoped-bookmarks.json"), nil
}

func saveSecurityBookmark(storePath, key, kind, path string) (securityBookmarkStoreReport, error) {
	if key == "" {
		return securityBookmarkStoreReport{}, fmt.Errorf("bookmark key required")
	}
	if kind == "" {
		kind = "file"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return securityBookmarkStoreReport{}, fmt.Errorf("resolve bookmark path: %w", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return securityBookmarkStoreReport{}, fmt.Errorf("resolve bookmark path symlinks: %w", err)
	}
	bookmark, err := createSecurityScopedBookmark(abs)
	if err != nil {
		return securityBookmarkStoreReport{}, err
	}
	return saveSecurityBookmarkBytes(storePath, key, kind, abs, bookmark)
}

func saveSecurityBookmarkBytes(storePath, key, kind, path string, bookmark []byte) (securityBookmarkStoreReport, error) {
	if key == "" {
		return securityBookmarkStoreReport{}, fmt.Errorf("bookmark key required")
	}
	if len(bookmark) == 0 {
		return securityBookmarkStoreReport{}, fmt.Errorf("bookmark bytes required")
	}
	if kind == "" {
		kind = "file"
	}
	store, err := readSecurityBookmarkStore(storePath)
	if err != nil {
		return securityBookmarkStoreReport{}, err
	}
	entry := securityBookmarkEntry{
		Key:      key,
		Kind:     kind,
		Path:     path,
		Bookmark: base64.StdEncoding.EncodeToString(bookmark),
		Updated:  time.Now().UTC().Format(time.RFC3339),
	}
	store.Entries[key] = entry
	if err := writeSecurityBookmarkStore(storePath, store); err != nil {
		return securityBookmarkStoreReport{}, err
	}
	return securityBookmarkStoreReport{
		Action: "save",
		Store:  storePath,
		Key:    key,
		Entry:  securityBookmarkEntryForReport(entry),
	}, nil
}

func resolveSecurityBookmarkFromStore(storePath, key string) (securityBookmarkStoreReport, error) {
	access, err := resolveSecurityBookmarkAccessFromStore(storePath, key)
	if err != nil {
		return securityBookmarkStoreReport{}, err
	}
	defer access.Stop()
	proof, err := readSecurityScopedBookmarkProof(access.Entry.Path, access.Path, access.Stale, access.BookmarkSize)
	if err != nil {
		return securityBookmarkStoreReport{}, err
	}
	return securityBookmarkStoreReport{
		Action: "resolve",
		Store:  storePath,
		Key:    key,
		Entry:  securityBookmarkEntryForReport(access.Entry),
		Proof:  &proof,
	}, nil
}

func resolveSecurityBookmarkAccessFromStore(storePath, key string) (securityBookmarkAccess, error) {
	if key == "" {
		return securityBookmarkAccess{}, fmt.Errorf("bookmark key required")
	}
	entry, bookmark, err := readSecurityBookmarkBytesFromStore(storePath, key)
	if err != nil {
		return securityBookmarkAccess{}, err
	}
	resolved, stale, stop, err := resolveSecurityScopedBookmark(bookmark)
	if err != nil {
		return securityBookmarkAccess{}, err
	}
	if stop == nil {
		return securityBookmarkAccess{}, fmt.Errorf("security-scoped bookmark did not start access")
	}
	return securityBookmarkAccess{
		Entry:        entry,
		Path:         resolved,
		Stale:        stale,
		Stop:         stop,
		BookmarkSize: len(bookmark),
	}, nil
}

func readSecurityBookmarkBytesFromStore(storePath, key string) (securityBookmarkEntry, []byte, error) {
	if key == "" {
		return securityBookmarkEntry{}, nil, fmt.Errorf("bookmark key required")
	}
	store, err := readSecurityBookmarkStore(storePath)
	if err != nil {
		return securityBookmarkEntry{}, nil, err
	}
	entry, ok := store.Entries[key]
	if !ok {
		return securityBookmarkEntry{}, nil, fmt.Errorf("bookmark %q not found", key)
	}
	bookmark, err := base64.StdEncoding.DecodeString(entry.Bookmark)
	if err != nil {
		return securityBookmarkEntry{}, nil, fmt.Errorf("decode bookmark %q: %w", key, err)
	}
	return entry, bookmark, nil
}

func securityBookmarkEntryForReport(entry securityBookmarkEntry) securityBookmarkEntryReport {
	n := 0
	if bookmark, err := base64.StdEncoding.DecodeString(entry.Bookmark); err == nil {
		n = len(bookmark)
	}
	return securityBookmarkEntryReport{
		Key:          entry.Key,
		Kind:         entry.Kind,
		Path:         entry.Path,
		BookmarkSize: n,
		Updated:      entry.Updated,
	}
}

func readSecurityBookmarkStore(path string) (securityBookmarkStore, error) {
	store := securityBookmarkStore{
		Version: 1,
		Entries: make(map[string]securityBookmarkEntry),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return securityBookmarkStore{}, fmt.Errorf("read bookmark store: %w", err)
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return securityBookmarkStore{}, fmt.Errorf("parse bookmark store: %w", err)
	}
	if store.Version == 0 {
		store.Version = 1
	}
	if store.Entries == nil {
		store.Entries = make(map[string]securityBookmarkEntry)
	}
	return store, nil
}

func writeSecurityBookmarkStore(path string, store securityBookmarkStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create bookmark store dir: %w", err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bookmark store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bookmarks-*.tmp")
	if err != nil {
		return fmt.Errorf("create bookmark store temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write bookmark store temp: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod bookmark store temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close bookmark store temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace bookmark store: %w", err)
	}
	return nil
}
