//go:build darwin || linux

package firmware

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tmc/apple/x/plist"
)

type preparedState struct {
	SchemaVersion int               `json:"schemaVersion"`
	Stage         string            `json:"stage"`
	Attempt       string            `json:"attempt"`
	SourceCommit  string            `json:"sourceCommit"`
	Inputs        map[string]string `json:"inputs"`
	Outputs       map[string]string `json:"outputs"`
	Tree          string            `json:"tree"`
}

// Prepare merges explicit local iPhone and cloudOS IPSWs using the pinned recipe.
// Output holds attempt logs, retained archives and firmware.json. Existing
// prepared output is reused only after checking all recorded hashes. Failed
// attempts remain available for diagnosis; another call starts a new attempt.
// This prepares firmware bytes, not a VM, and does not install variant tools.
func Prepare(ctx context.Context, source, iphone, cloudos, output string, log io.Writer) error {
	if source == "" || iphone == "" || cloudos == "" || output == "" {
		return fmt.Errorf("source, iphone, cloudos and output paths are required")
	}
	report, err := InspectSource(ctx, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 {
		return fmt.Errorf("toolchain source: %s", strings.Join(report.Problems, "; "))
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	return prepare(ctx, iphone, cloudos, output, func(ctx context.Context, dir, cache, iphone, cloudos string, log io.Writer) error {
		return prepareRecipe(ctx, source, dir, cache, iphone, cloudos, log)
	}, log)
}

func prepare(ctx context.Context, iphone, cloudos, output string, run func(context.Context, string, string, string, string, io.Writer) error, log io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	info, err := os.Lstat(output)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("prepare output is not a directory")
	}
	lock, err := os.OpenFile(filepath.Join(output, ".prepare.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("lock firmware output: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	inputs := make(map[string]string)
	for key, name := range map[string]string{"iphone": iphone, "cloudos": cloudos} {
		digest, err := archiveDigest(ctx, name)
		if err != nil {
			return fmt.Errorf("%s archive: %w", key, err)
		}
		inputs[key] = digest
	}
	statePath := filepath.Join(output, "firmware.json")
	if data, err := os.ReadFile(statePath); err == nil {
		var old preparedState
		if err := json.Unmarshal(data, &old); err != nil {
			return fmt.Errorf("read prepared state: %w", err)
		}
		if old.SchemaVersion != 1 || old.Stage != "prepared" || old.SourceCommit != SourceCommit || !equalHashes(old.Inputs, inputs) {
			return fmt.Errorf("prepared output belongs to different inputs or recipe; choose a new output directory")
		}
		if !filepath.IsLocal(old.Tree) {
			return fmt.Errorf("invalid prepared tree path")
		}
		hashes, err := treeHashes(ctx, filepath.Join(output, old.Tree))
		if err != nil {
			return err
		}
		if !equalHashes(hashes, old.Outputs) {
			return fmt.Errorf("prepared tree differs from recorded hashes")
		}
		return validateTree(filepath.Join(output, old.Tree))
	} else if !os.IsNotExist(err) {
		return err
	}
	attempt, err := os.MkdirTemp(output, "attempt-")
	if err != nil {
		return err
	}
	// Every attempt has private archive names and extraction caches. The recipe
	// cannot select an older restore tree or trust a partially extracted cache.
	cache := filepath.Join(attempt, "archives")
	work := filepath.Join(attempt, "work")
	for _, dir := range []string{cache, work} {
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
	}
	state := preparedState{SchemaVersion: 1, Stage: "preparing", Attempt: filepath.Base(attempt), SourceCommit: SourceCommit, Inputs: inputs}
	if err := writeState(filepath.Join(attempt, "attempt.json"), state); err != nil {
		return err
	}
	for key, name := range map[string]string{"iphone": iphone, "cloudos": cloudos} {
		dst := filepath.Join(cache, map[string]string{"iphone": "iphone_Restore.ipsw", "cloudos": "cloudos.ipsw"}[key])
		if err := copyArchive(ctx, name, dst, inputs[key]); err != nil {
			return err
		}
	}
	logfile, err := os.OpenFile(filepath.Join(attempt, "prepare.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logfile.Close()
	var writer io.Writer = logfile
	if log != nil {
		writer = io.MultiWriter(logfile, log)
	}
	if err := run(ctx, work, cache, filepath.Join(cache, "iphone_Restore.ipsw"), filepath.Join(cache, "cloudos.ipsw"), writer); err != nil {
		return fmt.Errorf("prepare attempt %s: %w", state.Attempt, err)
	}
	tree := filepath.Join(work, "iphone_Restore")
	if err := validateTree(tree); err != nil {
		return fmt.Errorf("validate prepared tree: %w", err)
	}
	hashes, err := treeHashes(ctx, tree)
	if err != nil {
		return err
	}
	if err := syncTree(work); err != nil {
		return err
	}
	if err := logfile.Sync(); err != nil {
		return err
	}
	state.Stage = "prepared"
	state.Outputs = hashes
	state.Tree, err = filepath.Rel(output, tree)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeState(filepath.Join(attempt, "attempt.json"), state); err != nil {
		return err
	}
	return writeState(statePath, state)
}

func archiveDigest(ctx context.Context, name string) (string, error) {
	z, err := zip.OpenReader(name)
	if err != nil {
		return "", err
	}
	defer z.Close()
	seen := make(map[string]bool)
	for _, f := range z.File {
		n := strings.TrimSuffix(f.Name, "/")
		if !filepath.IsLocal(n) || strings.Contains(n, "\\") || filepath.ToSlash(filepath.Clean(n)) != n || seen[strings.ToLower(n)] || (!f.Mode().IsRegular() && !f.Mode().IsDir()) {
			return "", fmt.Errorf("unsafe or duplicate archive entry %q", f.Name)
		}
		seen[strings.ToLower(n)] = true
	}
	return fileDigest(ctx, name)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func fileDigest(ctx context.Context, name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyArchive(ctx context.Context, src, dst, want string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), contextReader{ctx, in})
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("archive changed while staging: %s", src)
	}
	return nil
}

func treeHashes(ctx context.Context, root string) (map[string]string, error) {
	hashes := make(map[string]string)
	err := filepath.WalkDir(root, func(name string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular prepared file %s", name)
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		hash, err := fileDigest(ctx, name)
		if err != nil {
			return err
		}
		hashes[filepath.ToSlash(rel)] = hash
		return nil
	})
	return hashes, err
}

func equalHashes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func writeState(name string, v preparedState) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), name); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validateTree(root string) error {
	var manifest struct {
		BuildIdentities []struct {
			ApBoardID string `plist:"ApBoardID"`
			ApChipID  string `plist:"ApChipID"`
			Info      struct {
				DeviceClass string `plist:"DeviceClass"`
			} `plist:"Info"`
			Manifest map[string]struct {
				Info struct {
					Path string `plist:"Path"`
				} `plist:"Info"`
			} `plist:"Manifest"`
		} `plist:"BuildIdentities"`
	}
	data, err := os.ReadFile(filepath.Join(root, "BuildManifest.plist"))
	if err != nil {
		return err
	}
	if _, err := plist.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if len(manifest.BuildIdentities) != 1 {
		return fmt.Errorf("expected one hybrid build identity")
	}
	bi := manifest.BuildIdentities[0]
	if bi.ApBoardID != "0x90" || bi.ApChipID != "0xFE01" || bi.Info.DeviceClass != "vresearch101ap" {
		return fmt.Errorf("hybrid identity does not match vresearch101ap")
	}
	for _, key := range []string{"LLB", "iBSS", "iBEC", "iBoot", "DeviceTree", "RestoreDeviceTree", "SEP", "RestoreSEP", "KernelCache", "RestoreKernelCache", "RestoreRamDisk", "OS", "SystemVolume", "StaticTrustCache", "Ap,SystemVolumeCanonicalMetadata", "Ap,RestoreSecurePageTableMonitor", "Ap,RestoreTrustedExecutionMonitor", "Ap,SecurePageTableMonitor", "Ap,TrustedExecutionMonitor", "RecoveryMode", "RestoreTrustCache"} {
		if _, ok := bi.Manifest[key]; !ok {
			return fmt.Errorf("missing hybrid component %s", key)
		}
	}
	for key, entry := range bi.Manifest {
		if !filepath.IsLocal(entry.Info.Path) {
			return fmt.Errorf("invalid component path for %s", key)
		}
		info, err := os.Lstat(filepath.Join(root, entry.Info.Path))
		if err != nil {
			return fmt.Errorf("component %s: %w", key, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("invalid component file for %s", key)
		}
	}
	for _, name := range []string{"Restore.plist", "iPhone-BuildManifest.plist"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		var v map[string]any
		if _, err := plist.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func syncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(name string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, name)
			return nil
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		err = f.Sync()
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		f, err := os.Open(dirs[i])
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
