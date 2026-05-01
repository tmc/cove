package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tmc/vz-macos/internal/ociimage"
)

const GCGrace = time.Hour

type Store struct {
	Dir string
}

type GCResult struct {
	Deleted   int
	Reclaimed int64
	KeptYoung int
}

func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "store")
}

func New(dir string) Store {
	if dir == "" {
		dir = DefaultDir()
	}
	return Store{Dir: dir}
}

func (s Store) Ensure(ctx context.Context, digest string, size int64, fetch func(context.Context) (io.ReadCloser, error)) error {
	if fetch == nil {
		return fmt.Errorf("store blob: nil fetch")
	}
	if err := s.VerifyBlob(digest, size); err == nil {
		_ = os.Chtimes(mustBlobPath(s.Dir, digest), time.Now(), time.Now())
		return nil
	}
	body, err := fetch(ctx)
	if err != nil {
		return err
	}
	defer body.Close()
	return s.Put(digest, size, body)
}

func (s Store) Put(digest string, size int64, r io.Reader) error {
	algo, hexDigest, err := splitDigest(digest)
	if err != nil {
		return err
	}
	if algo != "sha256" {
		return fmt.Errorf("store blob: unsupported digest %q", digest)
	}
	if size < 0 {
		return fmt.Errorf("store blob: negative size %d", size)
	}
	dir := filepath.Join(s.Dir, "blobs", algo)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create blob dir: %w", err)
	}
	path := filepath.Join(dir, hexDigest)
	if err := verifyFile(path, digest, size); err == nil {
		_ = os.Chtimes(path, time.Now(), time.Now())
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create blob temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	h := sha256.New()
	n, copyErr := io.Copy(tmp, io.TeeReader(r, h))
	if copyErr != nil {
		tmp.Close()
		return fmt.Errorf("write blob: %w", copyErr)
	}
	if n != size {
		tmp.Close()
		return fmt.Errorf("write blob: size %d, want %d", n, size)
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != digest {
		tmp.Close()
		return fmt.Errorf("write blob: digest %s, want %s", got, digest)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close blob: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename blob: %w", err)
	}
	return nil
}

func (s Store) OpenVerified(digest string, size int64) (*os.File, error) {
	path, err := s.BlobPath(digest)
	if err != nil {
		return nil, err
	}
	if err := verifyFile(path, digest, size); err != nil {
		return nil, err
	}
	_ = os.Chtimes(path, time.Now(), time.Now())
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}
	return f, nil
}

func (s Store) VerifyBlob(digest string, size int64) error {
	path, err := s.BlobPath(digest)
	if err != nil {
		return err
	}
	return verifyFile(path, digest, size)
}

func (s Store) BlobPath(digest string) (string, error) {
	algo, hexDigest, err := splitDigest(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, "blobs", algo, hexDigest), nil
}

func (s Store) StoreManifest(digest string, data []byte) error {
	_, hexDigest, err := splitDigest(digest)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Dir, "manifests", "sha256")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	path := filepath.Join(dir, hexDigest+".json")
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create manifest temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}

func (s Store) LockShared() (func() error, error) {
	return s.lock(unix.LOCK_SH)
}

func (s Store) LockExclusive() (func() error, error) {
	return s.lock(unix.LOCK_EX)
}

func (s Store) GC(reachable map[string]bool, grace time.Duration) (GCResult, error) {
	var result GCResult
	unlock, err := s.LockExclusive()
	if err != nil {
		return result, err
	}
	defer unlock()
	if grace <= 0 {
		grace = GCGrace
	}
	root := filepath.Join(s.Dir, "blobs", "sha256")
	now := time.Now()
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		digest := "sha256:" + filepath.Base(path)
		if reachable[digest] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < grace {
			result.KeptYoung++
			return nil
		}
		size := info.Size()
		if err := os.Remove(path); err != nil {
			return err
		}
		result.Deleted++
		result.Reclaimed += size
		return nil
	})
	if os.IsNotExist(err) {
		return result, nil
	}
	return result, err
}

func (s Store) ReachableFromVMs(vmsDir string) (map[string]bool, error) {
	reachable := map[string]bool{}
	entries, err := os.ReadDir(vmsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return reachable, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(vmsDir, entry.Name(), "disk.provenance"))
		if err != nil {
			continue
		}
		digest := strings.TrimSpace(string(data))
		if digest == "" {
			continue
		}
		if err := s.markManifest(reachable, digest); err != nil {
			return nil, err
		}
	}
	return reachable, nil
}

func (s Store) ReachableFromBuildCache() (map[string]bool, error) {
	reachable := map[string]bool{}
	root := filepath.Join(s.Dir, "build-cache")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("parse build cache %s: %w", path, err)
		}
		markDigestStrings(reachable, v)
		return nil
	})
	if os.IsNotExist(err) {
		return reachable, nil
	}
	if err != nil {
		return reachable, err
	}
	if err := s.expandBuildCacheReferences(reachable); err != nil {
		return nil, err
	}
	return reachable, nil
}

func markDigestStrings(reachable map[string]bool, v any) {
	switch v := v.(type) {
	case string:
		if _, _, err := splitDigest(v); err == nil {
			reachable[v] = true
		}
	case []any:
		for _, x := range v {
			markDigestStrings(reachable, x)
		}
	case map[string]any:
		for _, x := range v {
			markDigestStrings(reachable, x)
		}
	}
}

func (s Store) expandBuildCacheReferences(reachable map[string]bool) error {
	var digests []string
	for digest := range reachable {
		digests = append(digests, digest)
	}
	for _, digest := range digests {
		if err := s.markManifest(reachable, digest); err != nil {
			return err
		}
		if err := s.markBuildLayer(reachable, digest); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) markBuildLayer(reachable map[string]bool, digest string) error {
	_, hexDigest, err := splitDigest(digest)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "build-cache", "layers", hexDigest+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("parse build layer %s: %w", digest, err)
	}
	markDigestStrings(reachable, v)
	return nil
}

func (s Store) markManifest(reachable map[string]bool, digest string) error {
	_, hexDigest, err := splitDigest(digest)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "manifests", "sha256", hexDigest+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var manifest ociimage.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse stored manifest %s: %w", digest, err)
	}
	for _, desc := range manifest.Layers {
		if desc.Digest != "" {
			reachable[desc.Digest] = true
		}
	}
	if manifest.Config.Digest != "" {
		reachable[manifest.Config.Digest] = true
	}
	return nil
}

func (s Store) lock(kind int) (func() error, error) {
	if err := os.MkdirAll(s.Dir, 0755); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "gc.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open gc lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), kind); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock store: %w", err)
	}
	return func() error {
		err := unix.Flock(int(f.Fd()), unix.LOCK_UN)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}, nil
}

func verifyFile(path, digest string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return fmt.Errorf("verify blob: %w", err)
	}
	if n != size {
		return fmt.Errorf("verify blob: size %d, want %d", n, size)
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != digest {
		return fmt.Errorf("verify blob: digest %s, want %s", got, digest)
	}
	return nil
}

func splitDigest(digest string) (string, string, error) {
	algo, hexDigest, ok := strings.Cut(digest, ":")
	if !ok || algo == "" || hexDigest == "" {
		return "", "", fmt.Errorf("invalid digest %q", digest)
	}
	if algo != "sha256" {
		return "", "", fmt.Errorf("unsupported digest %q", digest)
	}
	if len(hexDigest) != sha256.Size*2 {
		return "", "", fmt.Errorf("invalid digest %q", digest)
	}
	if _, err := hex.DecodeString(hexDigest); err != nil {
		return "", "", fmt.Errorf("invalid digest %q", digest)
	}
	return algo, hexDigest, nil
}

func mustBlobPath(root, digest string) string {
	_, hexDigest, _ := strings.Cut(digest, ":")
	return filepath.Join(root, "blobs", "sha256", hexDigest)
}
