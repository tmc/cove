package firmware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Bundle is a verified patched output held under a shared patch-workflow lease.
// Keep it open until all consumers of its files finish. The lease excludes Cove's
// patch workflow, not unrelated filesystem writers. Verification checks receipt
// consistency, not cryptographic provenance or boot eligibility.
// The zero value is closed; use OpenPatched to open a bundle.
type Bundle struct {
	mu     sync.RWMutex
	root   *os.Root
	lock   *os.File
	hashes map[string]string
}

// OpenPatched opens a published Patch output without rerunning the patcher. It
// verifies the pinned source marker, complete output catalog, records and log,
// and confines file access to the recorded work tree. Original toolchain/input
// paths need not remain present. Close releases the workflow lease.
func OpenPatched(ctx context.Context, directory string) (_ *Bundle, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if directory == "" {
		return nil, fmt.Errorf("patched bundle directory is required")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	lock, err := root.OpenFile(".patch.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			lock.Close()
		}
	}()
	info, err := lock.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("nonregular patch lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("lease patched bundle: %w", err)
	}
	file, err := root.OpenFile("patched.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("nonregular patched receipt"), file.Close())
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, file}, 16<<20+1))
	err = errors.Join(err, file.Close())
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("patched receipt exceeds 16 MiB")
	}
	var state patchedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.SchemaVersion != 1 || state.Stage != "patched" || state.Patcher.SourceCommit != SourceCommit || !safeBundlePath(state.Attempt) || !strings.HasPrefix(state.Attempt, "attempt-") || strings.Contains(state.Attempt, "/") || state.Tree != state.Attempt+"/work" || len(state.Outputs) == 0 {
		return nil, fmt.Errorf("invalid patched bundle receipt")
	}
	for name, hash := range state.Outputs {
		if !safeBundlePath(name) || !validBundleHash(hash) {
			return nil, fmt.Errorf("invalid patched output entry %q", name)
		}
	}
	for name, hash := range map[string]string{"records.json": state.RecordsSHA256, "patch.log": state.LogSHA256} {
		if !validBundleHash(hash) {
			return nil, fmt.Errorf("invalid patched %s hash", name)
		}
		f, err := openBundleFile(ctx, root, state.Attempt+"/"+name, hash)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	work, err := root.OpenRoot(state.Tree)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			work.Close()
		}
	}()
	seen := 0
	err = fs.WalkDir(work.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("nonregular patched file %s", name)
		}
		want, ok := state.Outputs[name]
		if !ok {
			return fmt.Errorf("unrecorded patched file %s", name)
		}
		f, err := openBundleFile(ctx, work, name, want)
		if err != nil {
			return err
		}
		seen++
		return f.Close()
	})
	if err != nil {
		return nil, err
	}
	if seen != len(state.Outputs) {
		return nil, fmt.Errorf("patched output catalog has missing files")
	}
	if _, ok := state.Outputs["iphone_Restore/BuildManifest.plist"]; !ok {
		return nil, fmt.Errorf("patched bundle has no build manifest")
	}
	return &Bundle{root: work, lock: lock, hashes: state.Outputs}, nil
}

// Open opens a catalogued regular file relative to the patched work tree,
// verifies its SHA-256 through that handle, then rewinds it. The caller closes
// the returned file and must finish reading before closing Bundle.
func (b *Bundle) Open(ctx context.Context, name string) (*os.File, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.root == nil {
		return nil, fmt.Errorf("patched bundle is closed")
	}
	if !safeBundlePath(name) {
		return nil, fmt.Errorf("invalid patched file path")
	}
	want, ok := b.hashes[name]
	if !ok {
		return nil, fmt.Errorf("file %s is not in patched output catalog", name)
	}
	return openBundleFile(ctx, b.root, name, want)
}

// Close releases the bundle's directory handle and patch-workflow lease.
func (b *Bundle) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.root == nil {
		return nil
	}
	err := errors.Join(b.root.Close(), b.lock.Close())
	b.root, b.lock = nil, nil
	return err
}

func openBundleFile(ctx context.Context, root *os.Root, name, want string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) { return nil, errors.Join(err, f.Close()) }
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(fmt.Errorf("nonregular patched file %s", name))
	}
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return fail(err)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fail(fmt.Errorf("patched file %s differs from recorded hash", name))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return f, nil
}

func safeBundlePath(name string) bool {
	return filepath.IsLocal(name) && name != "." && filepath.ToSlash(filepath.Clean(name)) == name && !strings.Contains(name, "\\")
}
func validBundleHash(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == sha256.Size && strings.ToLower(hash) == hash
}
