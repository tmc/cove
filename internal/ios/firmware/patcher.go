//go:build darwin || linux

package firmware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Patcher describes a built host tool. It does not establish firmware or boot
// compatibility. Binary is a content-addressed executable in the tool cache.
type Patcher struct {
	SourceCommit  string `json:"sourceCommit"`
	OverlaySHA256 string `json:"overlaySHA256"`
	DeveloperDir  string `json:"developerDir"`
	SwiftVersion  string `json:"swiftVersion"`
	SDKPath       string `json:"sdkPath"`
	SDKVersion    string `json:"sdkVersion"`
	Binary        string `json:"binary"`
	SHA256        string `json:"sha256"`
}

// BuildPatcher builds the pinned Swift patcher with Cove's compiler overlay
// for journaled mount operations. It uses directory/source and writes
// directory/patcher.json after checking the executable's command interface.
// Developer selects an Xcode developer directory for this operation only; an
// empty value uses the current selection. Swift's incremental build cache is
// retained. The caller controls cancellation and the output writer may be nil.
func BuildPatcher(ctx context.Context, directory, developer string, log io.Writer) (Patcher, error) {
	var report Patcher
	if directory == "" {
		return report, fmt.Errorf("toolchain directory is required")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return report, err
	}
	source := filepath.Join(directory, "source")
	state, err := InspectSource(ctx, source)
	if err != nil {
		return report, err
	}
	if len(state.Problems) != 0 {
		return report, fmt.Errorf("toolchain source: %s", strings.Join(state.Problems, "; "))
	}
	lock, err := os.OpenFile(filepath.Join(directory, ".patcher.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return report, fmt.Errorf("lock patcher build: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if developer != "" {
		developer, err = filepath.Abs(developer)
		if err != nil {
			return report, err
		}
	}
	command := func(name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = source
		cmd.Env = os.Environ()
		if developer != "" {
			cmd.Env = append(cmd.Env, "DEVELOPER_DIR="+developer)
		}
		return cmd
	}
	capture := func(name string, args ...string) (string, error) {
		out := &boundedOutput{limit: 1 << 20}
		cmd := command(name, args...)
		cmd.Stdout, cmd.Stderr = out, out
		if err := runPatcherCommand(ctx, cmd); err != nil {
			return "", fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(out.data)))
		}
		if out.overflow {
			return "", fmt.Errorf("tool description exceeds limit")
		}
		return strings.TrimSpace(string(out.data)), nil
	}
	report.SourceCommit = SourceCommit
	report.DeveloperDir = developer
	if report.DeveloperDir == "" {
		report.DeveloperDir = os.Getenv("DEVELOPER_DIR")
	}
	if report.DeveloperDir == "" {
		report.DeveloperDir, err = capture("/usr/bin/xcode-select", "-p")
		if err != nil {
			return report, err
		}
	}
	report.SwiftVersion, err = capture("/usr/bin/xcrun", "swift", "--version")
	if err != nil {
		return report, err
	}
	report.SDKPath, err = capture("/usr/bin/xcrun", "--show-sdk-path")
	if err != nil {
		return report, err
	}
	report.SDKVersion, err = capture("/usr/bin/xcrun", "--show-sdk-version")
	if err != nil {
		return report, err
	}
	// Match the pinned Makefile's generated file without changing tracked sources.
	generated := []byte("// Auto-generated — do not edit\nenum VPhoneBuildInfo { static let commitHash = \"" + SourceCommit[:7] + "\" }\n")
	infoPath := filepath.Join(source, "sources/vphone-cli/VPhoneBuildInfo.swift")
	if err := writeBuildInfo(infoPath, generated); err != nil {
		return report, err
	}
	overlay, digest, err := preparePatcherOverlay(ctx, directory, source)
	if err != nil {
		return report, err
	}
	report.OverlaySHA256 = digest
	cmd := command("/usr/bin/xcrun", "swift", "build", "--product", "vphone-cli", "-Xswiftc", "-vfsoverlay", "-Xswiftc", overlay)
	cmd.Stdout, cmd.Stderr = log, log
	if err := runPatcherCommand(ctx, cmd); err != nil {
		return report, fmt.Errorf("build Swift patcher (developer directory %s): %w", report.DeveloperDir, err)
	}
	built := filepath.Join(source, ".build/debug/vphone-cli")
	help, err := capture(built, "patch-firmware", "--help")
	if err != nil {
		return report, fmt.Errorf("check patcher command: %w", err)
	}
	for _, option := range []string{"--vm-directory", "--variant", "--records-out", "--force-exc-guard", "--no-binpack", "--no-vphoned", "--frida"} {
		if !strings.Contains(help, option) {
			return report, fmt.Errorf("patcher command missing %s", option)
		}
	}
	report.SHA256, err = fileDigest(ctx, built)
	if err != nil {
		return report, err
	}
	tools := filepath.Join(directory, "tools")
	if err := os.MkdirAll(tools, 0755); err != nil {
		return report, err
	}
	report.Binary = filepath.Join(tools, "vphone-patcher-"+report.SHA256)
	if _, err := os.Stat(report.Binary); os.IsNotExist(err) {
		tmp, err := os.MkdirTemp(tools, ".patcher-")
		if err != nil {
			return report, err
		}
		defer os.RemoveAll(tmp)
		staged := filepath.Join(tmp, "patcher")
		if err := copyArchive(ctx, built, staged, report.SHA256); err != nil {
			return report, err
		}
		if err := os.Chmod(staged, 0755); err != nil {
			return report, err
		}
		if err := syncTree(tmp); err != nil {
			return report, err
		}
		if err := os.Rename(staged, report.Binary); err != nil {
			return report, err
		}
	} else if err != nil {
		return report, err
	}
	// Persist the published directory entry before the receipt points at it.
	toolDir, err := os.Open(tools)
	if err != nil {
		return report, err
	}
	syncErr := toolDir.Sync()
	closeErr := toolDir.Close()
	if syncErr != nil {
		return report, syncErr
	}
	if closeErr != nil {
		return report, closeErr
	}
	after, err := InspectSource(ctx, source)
	if err != nil {
		return report, err
	}
	if len(after.Problems) != 0 {
		return report, fmt.Errorf("toolchain source changed during build: %s", strings.Join(after.Problems, "; "))
	}
	if err := writeBuildInfo(infoPath, generated); err != nil {
		return report, err
	}
	hash, err := fileDigest(ctx, report.Binary)
	if err != nil {
		return report, err
	}
	if hash != report.SHA256 {
		return report, fmt.Errorf("cached patcher digest mismatch")
	}
	if _, err := capture(report.Binary, "patch-firmware", "--help"); err != nil {
		return report, fmt.Errorf("check published patcher: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, writeState(filepath.Join(directory, "patcher.json"), report)
}

func writeBuildInfo(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		old, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if string(old) != string(data) {
			return fmt.Errorf("generated build info differs from pinned source; remove it before rebuilding")
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func runPatcherCommand(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}
