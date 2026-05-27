//go:build darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/tmc/apple/foundation"
)

type securityScopedBookmarkReport struct {
	Action       string `json:"action"`
	AppSandbox   bool   `json:"apple_app_sandbox"`
	Path         string `json:"path"`
	ResolvedPath string `json:"resolved_path"`
	BookmarkSize int    `json:"bookmark_bytes"`
	Stale        bool   `json:"stale"`
	Started      bool   `json:"started_access"`
	ReadBytes    int    `json:"read_bytes"`
	SHA256       string `json:"sha256"`
}

func securityScopedBookmarkRoundTrip(path string) (securityScopedBookmarkReport, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return securityScopedBookmarkReport{}, fmt.Errorf("resolve bookmark path: %w", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return securityScopedBookmarkReport{}, fmt.Errorf("resolve bookmark path symlinks: %w", err)
	}
	bookmark, err := createSecurityScopedBookmark(abs)
	if err != nil {
		return securityScopedBookmarkReport{}, err
	}
	resolved, stale, stop, err := resolveSecurityScopedBookmark(bookmark)
	if err != nil {
		return securityScopedBookmarkReport{}, err
	}
	if stop == nil {
		return securityScopedBookmarkReport{}, fmt.Errorf("security-scoped bookmark did not start access")
	}
	defer stop()

	payload, err := os.ReadFile(resolved)
	if err != nil {
		return securityScopedBookmarkReport{}, fmt.Errorf("read resolved bookmark path: %w", err)
	}
	sum := sha256.Sum256(payload)
	return securityScopedBookmarkReport{
		Action:       "bookmark-probe",
		AppSandbox:   appleAppSandboxActive(),
		Path:         abs,
		ResolvedPath: resolved,
		BookmarkSize: len(bookmark),
		Stale:        stale,
		Started:      true,
		ReadBytes:    len(payload),
		SHA256:       hex.EncodeToString(sum[:]),
	}, nil
}

func createSecurityScopedBookmark(path string) ([]byte, error) {
	url := foundation.NewURLFileURLWithPath(path)
	if url.GetID() == 0 {
		return nil, fmt.Errorf("create file URL for bookmark: nil NSURL")
	}
	data, err := url.BookmarkDataWithOptionsIncludingResourceValuesForKeysRelativeToURLError(
		foundation.NSURLBookmarkCreationWithSecurityScope,
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create security-scoped bookmark: %w", err)
	}
	return copyNSData(data), nil
}

func resolveSecurityScopedBookmark(bookmark []byte) (path string, stale bool, stop func(), err error) {
	if len(bookmark) == 0 {
		return "", false, nil, fmt.Errorf("resolve security-scoped bookmark: empty bookmark")
	}
	data := foundation.NewDataWithBytesLength(bookmark)
	url, err := foundation.NewURLByResolvingBookmarkDataOptionsRelativeToURLBookmarkDataIsStaleError(
		data,
		foundation.NSURLBookmarkResolutionWithSecurityScope|foundation.NSURLBookmarkResolutionWithoutUI,
		nil,
		&stale,
	)
	if err != nil {
		return "", false, nil, fmt.Errorf("resolve security-scoped bookmark: %w", err)
	}
	if url.GetID() == 0 {
		return "", false, nil, fmt.Errorf("resolve security-scoped bookmark: nil NSURL")
	}
	if !url.StartAccessingSecurityScopedResource() {
		return "", stale, nil, fmt.Errorf("start security-scoped bookmark access: denied")
	}
	return url.Path(), stale, url.StopAccessingSecurityScopedResource, nil
}

func copyNSData(data foundation.INSData) []byte {
	n := data.Length()
	if n == 0 {
		return nil
	}
	ptr := data.Bytes()
	if ptr == nil {
		return nil
	}
	src := unsafe.Slice((*byte)(ptr), int(n))
	out := make([]byte, len(src))
	copy(out, src)
	return out
}
