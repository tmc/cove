package firmware

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/apple/x/plist"
)

// PatchOptions selects the pinned firmware pipeline. Prepared is a directory
// produced by Prepare; ROM is an explicit AVPBooter image. Output is a separate
// directory for immutable results and retained attempts. Helper is a signed
// Cove executable. Less also requires a provisioned Python and sealing tool directory.
type PatchOptions struct {
	Toolchain     string `json:"toolchain"`
	Prepared      string `json:"prepared"`
	ROM           string `json:"rom"`
	Output        string `json:"-"`
	Helper        string `json:"helper"`
	Variant       string `json:"variant"`
	NoBinpack     bool   `json:"noBinpack"`
	NoVphoned     bool   `json:"noVphoned"`
	ForceExcGuard bool   `json:"forceExcGuard"`
	Frida         bool   `json:"frida"`
	Quiet         bool   `json:"-"`
	Python        string `json:"python,omitempty"`
	SealDirectory string `json:"sealDirectory,omitempty"`
}

type patchedState struct {
	SchemaVersion int               `json:"schemaVersion"`
	Stage         string            `json:"stage"`
	Attempt       string            `json:"attempt"`
	Options       PatchOptions      `json:"options"`
	Patcher       Patcher           `json:"patcher"`
	Inputs        map[string]string `json:"inputs"`
	Outputs       map[string]string `json:"outputs,omitempty"`
	Tree          string            `json:"tree,omitempty"`
	RecordsSHA256 string            `json:"recordsSHA256,omitempty"`
	LogSHA256     string            `json:"logSHA256,omitempty"`
	Error         string            `json:"error,omitempty"`
}

func (o PatchOptions) validate() error {
	switch o.Variant {
	case "less", "regular", "dev", "jb", "exp":
	default:
		return fmt.Errorf("unknown firmware variant %q", o.Variant)
	}
	if (o.NoBinpack || o.NoVphoned) && o.Variant != "less" {
		return fmt.Errorf("no-binpack and no-vphoned require less")
	}
	if o.Frida && o.Variant != "jb" && o.Variant != "exp" {
		return fmt.Errorf("frida requires jb or exp")
	}
	if o.ForceExcGuard && (o.Variant == "less" || o.Variant == "dev") {
		return fmt.Errorf("force-exc-guard requires regular, jb or exp")
	}
	if o.Toolchain == "" || o.Prepared == "" || o.ROM == "" || o.Output == "" || o.Helper == "" {
		return fmt.Errorf("toolchain, prepared, rom, output and helper paths are required")
	}
	if o.Variant == "less" && (o.Python == "" || o.SealDirectory == "") {
		return fmt.Errorf("less requires explicit python and seal directory")
	}
	return nil
}

// Patch runs the selected variant on private copies, recovers owned mounts, and
// publishes patched.json only after validation. Existing results are verified
// before reuse. Failed attempts retain their logs and journal; a retry recovers
// those journals before starting another child. It never launches a VM.
func Patch(ctx context.Context, options PatchOptions, log io.Writer) error {
	if err := options.validate(); err != nil {
		return err
	}
	for _, p := range []*string{&options.Toolchain, &options.Prepared, &options.ROM, &options.Output, &options.Helper, &options.Python, &options.SealDirectory} {
		if *p == "" {
			continue
		}
		abs, err := filepath.Abs(*p)
		if err != nil {
			return err
		}
		*p = abs
	}
	tool, err := inspectPatcher(ctx, options.Toolchain)
	if err != nil {
		return err
	}
	helper, err := os.Stat(options.Helper)
	if err != nil {
		return err
	}
	if !helper.Mode().IsRegular() || helper.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("mount helper is not executable")
	}
	if options.Variant == "less" {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		probe := exec.CommandContext(probeCtx, options.Python, "-c", "from ipsw_parser.ipsw import IPSW; import capstone; from keystone import Ks, KS_ARCH_ARM64, KS_MODE_LITTLE_ENDIAN; assert hasattr(IPSW, 'create_from_path'); assert Ks(KS_ARCH_ARM64, KS_MODE_LITTLE_ENDIAN).asm('nop')[0]")
		if err := runPatcherCommand(probeCtx, probe); err != nil {
			return fmt.Errorf("less python dependency probe: %w", err)
		}
	}
	return patch(ctx, options, tool, func(ctx context.Context, attempt, work string, lease *os.File, log io.Writer) error {
		args := []string{"patch-firmware", "--vm-directory", work, "--variant", options.Variant, "--records-out", filepath.Join(attempt, "records.json")}
		for _, flag := range []struct {
			set  bool
			name string
		}{{options.NoBinpack, "--no-binpack"}, {options.NoVphoned, "--no-vphoned"}, {options.ForceExcGuard, "--force-exc-guard"}, {options.Frida, "--frida"}} {
			if flag.set {
				args = append(args, flag.name)
			}
		}
		// Keep upstream verbose output for validation, even when the caller is quiet.
		cmd := exec.CommandContext(ctx, tool.Binary, args...)
		// Keep the workflow lock held if Cove dies before its patcher exits.
		cmd.ExtraFiles = []*os.File{lease}
		cmd.Dir = filepath.Join(options.Toolchain, "source")
		cmd.Env = os.Environ()
		for _, pair := range []string{"COVE_MOUNT_HELPER=" + options.Helper, "COVE_MOUNT_JOURNAL=" + filepath.Join(attempt, "mounts"), "TMPDIR=" + filepath.Join(attempt, "tmp"), "VPHONE_ROOT=" + filepath.Join(attempt, "cache"), "DEVELOPER_DIR=" + tool.DeveloperDir, "VPHONE_PYTHON=" + options.Python, "VPHONE_SEAL_DIR=" + options.SealDirectory} {
			cmd.Env = append(cmd.Env, pair)
		}
		cmd.Stdout, cmd.Stderr = log, log
		runErr := runPatcherCommand(ctx, cmd)
		verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		after, verifyErr := inspectPatcher(verifyCtx, options.Toolchain)
		if verifyErr == nil && after != tool {
			verifyErr = fmt.Errorf("patcher receipt changed during patching")
		}
		return errors.Join(runErr, verifyErr)
	}, recoverPatchMounts, log)
}

func inspectPatcher(ctx context.Context, directory string) (Patcher, error) {
	var tool Patcher
	data, err := os.ReadFile(filepath.Join(directory, "patcher.json"))
	if err != nil {
		return tool, err
	}
	if err := json.Unmarshal(data, &tool); err != nil {
		return tool, err
	}
	source := filepath.Join(directory, "source")
	state, err := InspectSource(ctx, source)
	if err != nil {
		return tool, err
	}
	if len(state.Problems) != 0 {
		return tool, fmt.Errorf("toolchain source: %s", strings.Join(state.Problems, "; "))
	}
	original, err := os.ReadFile(filepath.Join(source, "sources/FirmwarePatcher/Filesystem/CryptexFilesystemPatcher.swift"))
	if err != nil {
		return tool, err
	}
	patched, err := patchMountSource(string(original))
	if err != nil {
		return tool, err
	}
	if tool.SourceCommit != SourceCommit || tool.OverlaySHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(patched))) {
		return tool, fmt.Errorf("patcher receipt does not match current source and mount overlay; rebuild patcher")
	}
	digest, err := fileDigest(ctx, tool.Binary)
	if err != nil {
		return tool, err
	}
	if digest != tool.SHA256 {
		return tool, fmt.Errorf("patcher binary differs from receipt")
	}
	return tool, nil
}

func patch(ctx context.Context, options PatchOptions, tool Patcher, run func(context.Context, string, string, *os.File, io.Writer) error, recover func(string) error, log io.Writer) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(options.Output, 0700); err != nil {
		return err
	}
	output, err := filepath.EvalSymlinks(options.Output)
	if err != nil {
		return err
	}
	prepared, err := filepath.EvalSymlinks(options.Prepared)
	if err != nil {
		return err
	}
	for _, paths := range [][2]string{{prepared, output}, {output, prepared}} {
		rel, err := filepath.Rel(paths[0], paths[1])
		if err != nil {
			return err
		}
		if filepath.IsLocal(rel) {
			return fmt.Errorf("patch output and prepared directory must not overlap")
		}
	}
	lock, err := os.OpenFile(filepath.Join(output, ".patch.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("lock patch output: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	attempts, err := filepath.Glob(filepath.Join(output, "attempt-*"))
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if err := recover(filepath.Join(attempt, "mounts")); err != nil {
			return fmt.Errorf("recover %s: %w", filepath.Base(attempt), err)
		}
	}
	data, err := os.ReadFile(filepath.Join(prepared, "firmware.json"))
	if err != nil {
		return err
	}
	var input preparedState
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	if input.SchemaVersion != 1 || input.Stage != "prepared" || input.SourceCommit != SourceCommit || !filepath.IsLocal(input.Tree) || len(input.Outputs) == 0 {
		return fmt.Errorf("invalid prepared firmware receipt")
	}
	tree := filepath.Join(prepared, input.Tree)
	hashes, err := treeHashes(ctx, tree)
	if err != nil {
		return err
	}
	if !equalHashes(hashes, input.Outputs) {
		return fmt.Errorf("prepared tree differs from recorded hashes")
	}
	if err := validateTree(tree); err != nil {
		return err
	}
	if options.Frida {
		data, err := os.ReadFile(filepath.Join(tree, "BuildManifest.plist"))
		if err != nil {
			return err
		}
		var manifest struct {
			ProductVersion string `plist:"ProductVersion"`
		}
		if _, err := plist.Unmarshal(data, &manifest); err != nil {
			return err
		}
		parts := strings.Split(manifest.ProductVersion, ".")
		if len(parts) < 2 {
			return fmt.Errorf("frida requires cloudOS 26.4 or later")
		}
		major, e1 := strconv.Atoi(parts[0])
		minor, e2 := strconv.Atoi(parts[1])
		if e1 != nil || e2 != nil || major < 26 || (major == 26 && minor < 4) {
			return fmt.Errorf("frida requires cloudOS 26.4 or later")
		}
	}
	romInfo, err := os.Stat(options.ROM)
	if err != nil {
		return err
	}
	if !romInfo.Mode().IsRegular() || romInfo.Size() == 0 {
		return fmt.Errorf("rom must be a nonempty regular file")
	}
	romHash, err := fileDigest(ctx, options.ROM)
	if err != nil {
		return err
	}
	inputs := map[string]string{"preparedReceipt": fmt.Sprintf("%x", sha256.Sum256(data)), "rom": romHash}
	for key, name := range map[string]string{"helper": options.Helper, "python": options.Python} {
		if name == "" {
			continue
		}
		digest, err := fileDigest(ctx, name)
		if err != nil {
			return err
		}
		inputs[key] = digest
	}
	if options.Variant == "less" && options.SealDirectory != "" {
		data, err := os.ReadFile(filepath.Join(tree, "BuildManifest.plist"))
		if err != nil {
			return err
		}
		var manifest struct {
			ProductVersion string `plist:"ProductVersion"`
		}
		if _, err := plist.Unmarshal(data, &manifest); err != nil {
			return err
		}
		version := manifest.ProductVersion
		if version == "" || strings.ContainsAny(version, "/\\") {
			return fmt.Errorf("invalid sealing tool version")
		}
		seal := filepath.Join(options.SealDirectory, "apfs_sealvolume_"+version)
		info, err := os.Stat(seal)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("sealing tool is not executable")
		}
		inputs["sealvolume"], err = fileDigest(ctx, seal)
		if err != nil {
			return err
		}
	}
	receipt := filepath.Join(output, "patched.json")
	if data, err := os.ReadFile(receipt); err == nil {
		var old patchedState
		if err := json.Unmarshal(data, &old); err != nil {
			return err
		}
		recorded := options
		recorded.Output = ""
		recorded.Quiet = false
		if old.SchemaVersion != 1 || old.Stage != "patched" || old.Options != recorded || old.Patcher != tool || !equalHashes(old.Inputs, inputs) || !filepath.IsLocal(old.Tree) || !filepath.IsLocal(old.Attempt) {
			return fmt.Errorf("patched output belongs to different inputs or options; choose a new output directory")
		}
		hashes, err := treeHashes(ctx, filepath.Join(output, old.Tree))
		if err != nil {
			return err
		}
		if !equalHashes(hashes, old.Outputs) {
			return fmt.Errorf("patched tree differs from recorded hashes")
		}
		for name, want := range map[string]string{"records.json": old.RecordsSHA256, "patch.log": old.LogSHA256} {
			got, err := fileDigest(ctx, filepath.Join(output, old.Attempt, name))
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("patched %s differs from receipt", name)
			}
		}
		return validateTree(filepath.Join(output, old.Tree, "iphone_Restore"))
	} else if !os.IsNotExist(err) {
		return err
	}
	attempt, err := os.MkdirTemp(output, "attempt-")
	if err != nil {
		return err
	}
	work := filepath.Join(attempt, "work")
	for _, name := range []string{work, filepath.Join(work, "iphone_Restore"), filepath.Join(attempt, "mounts"), filepath.Join(attempt, "tmp"), filepath.Join(attempt, "cache")} {
		if err := os.Mkdir(name, 0700); err != nil {
			return err
		}
	}
	state := patchedState{SchemaVersion: 1, Stage: "patching", Attempt: filepath.Base(attempt), Options: options, Patcher: tool, Inputs: inputs}
	state.Options.Output = ""
	state.Options.Quiet = false
	statePath := filepath.Join(attempt, "attempt.json")
	if err := writeState(statePath, state); err != nil {
		return err
	}
	// Persist the attempt's directory entry before a child can attach an image.
	outputDir, err := os.Open(output)
	if err != nil {
		return err
	}
	syncErr := outputDir.Sync()
	closeErr := outputDir.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			state.Stage = "failed"
			state.Error = result.Error()
			result = errors.Join(result, writeState(statePath, state))
		}
	}()
	for name, want := range input.Outputs {
		dst := filepath.Join(work, "iphone_Restore", name)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if err := copyArchive(ctx, filepath.Join(tree, name), dst, want); err != nil {
			return err
		}
	}
	if err := copyArchive(ctx, options.ROM, filepath.Join(work, "AVPBooter.bin"), romHash); err != nil {
		return err
	}
	if err := syncTree(work); err != nil {
		return err
	}
	before, err := treeHashes(ctx, work)
	if err != nil {
		return err
	}
	logfile, err := os.OpenFile(filepath.Join(attempt, "patch.log"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer logfile.Close()
	var writer io.Writer = logfile
	if log != nil && !options.Quiet {
		writer = io.MultiWriter(logfile, log)
	}
	runErr := run(ctx, attempt, work, lock, writer)
	// The child has exited before cleanup. Cancellation must not cancel recovery.
	cleanupErr := recover(filepath.Join(attempt, "mounts"))
	if err := errors.Join(runErr, cleanupErr, logfile.Sync(), ctx.Err()); err != nil {
		return fmt.Errorf("patch attempt %s: %w", state.Attempt, err)
	}
	if err := validatePatchRecords(filepath.Join(attempt, "records.json"), options); err != nil {
		return err
	}
	if _, err := logfile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := validatePatchLog(logfile); err != nil {
		return err
	}
	if err := validateTree(filepath.Join(work, "iphone_Restore")); err != nil {
		return err
	}
	state.Outputs, err = treeHashes(ctx, work)
	if err != nil {
		return err
	}
	if equalHashes(before, state.Outputs) {
		return fmt.Errorf("patcher did not change firmware")
	}
	state.RecordsSHA256, err = fileDigest(ctx, filepath.Join(attempt, "records.json"))
	if err != nil {
		return err
	}
	state.LogSHA256, err = fileDigest(ctx, filepath.Join(attempt, "patch.log"))
	if err != nil {
		return err
	}
	state.Tree, err = filepath.Rel(output, work)
	if err != nil {
		return err
	}
	if err := syncTree(work); err != nil {
		return err
	}
	records, err := os.Open(filepath.Join(attempt, "records.json"))
	if err != nil {
		return err
	}
	syncErr = records.Sync()
	closeErr = records.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state.Stage = "patched"
	if err := writeState(statePath, state); err != nil {
		return err
	}
	return writeState(receipt, state)
}

func recoverPatchMounts(directory string) error {
	journal, err := OpenMountJournal(directory)
	if err != nil {
		return err
	}
	defer journal.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return journal.Recover(ctx)
}

type patchRecord struct {
	PatchID       string `json:"patchID"`
	Component     string `json:"component"`
	FileOffset    *int64 `json:"fileOffset"`
	OriginalBytes []byte `json:"originalBytes"`
	PatchedBytes  []byte `json:"patchedBytes"`
}

func validatePatchRecords(name string, options PatchOptions) error {
	variant := options.Variant
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return fmt.Errorf("invalid or oversized patch records")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 64<<20))
	var records []patchRecord
	if err := decoder.Decode(&records); err != nil {
		return fmt.Errorf("patch records: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing or oversized patch records")
	}
	components := map[string]bool{}
	ids := map[string]bool{}
	for _, r := range records {
		if r.PatchID == "" || r.FileOffset == nil || *r.FileOffset < 0 || r.OriginalBytes == nil || r.PatchedBytes == nil {
			return fmt.Errorf("invalid patch record")
		}
		special := r.PatchID == "filesystem.cryptex.merge" || r.PatchID == "manifest.hash"
		if special {
			if variant != "less" || r.Component != "" || len(r.OriginalBytes) != 0 || len(r.PatchedBytes) != 0 {
				return fmt.Errorf("unexpected filesystem or manifest record")
			}
		} else if r.Component == "" || len(r.PatchedBytes) == 0 {
			return fmt.Errorf("invalid byte patch record %s", r.PatchID)
		}
		components[r.Component] = true
		ids[r.PatchID] = true
	}
	required := []string{"ibec", "llb", "devicetree"}
	if variant == "less" {
		if !ids["filesystem.cryptex.merge"] || !ids["manifest.hash"] {
			return fmt.Errorf("missing filesystem or manifest patch record")
		}
	} else {
		required = append(required, "avpbooter", "ibss", "txm", "kernelcache")
	}
	for _, component := range required {
		if !components[component] {
			return fmt.Errorf("missing %s patch records", component)
		}
	}
	var prefixes []string
	if variant == "dev" || variant == "jb" || variant == "exp" {
		prefixes = append(prefixes, "txm_dev.")
	}
	if variant == "jb" || variant == "exp" {
		prefixes = append(prefixes, "ibss_jb.", "kernelcache_jb.")
	}
	if variant == "exp" {
		prefixes = append(prefixes, "kernelcache_exp.")
	}
	if options.Frida {
		prefixes = append(prefixes, "kernelcache_frida.")
	}
	if options.ForceExcGuard && !ids["kernel.thread_guard_violation"] {
		return fmt.Errorf("missing EXC_GUARD patch record")
	}
	for _, prefix := range prefixes {
		found := false
		for id := range ids {
			if strings.HasPrefix(id, prefix) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing %s patch records", prefix)
		}
	}
	return nil
}

func validatePatchLog(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "[-]") || strings.Contains(line, "Failed to copy:") {
			return fmt.Errorf("patcher reported incomplete work: %s", line)
		}
	}
	return scanner.Err()
}
