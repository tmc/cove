package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/vz-macos/internal/bytefmt"
	"golang.org/x/sys/unix"
)

// resolvePath resolves symlinks and returns the real path.
func resolvePath(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return absPath
	}
	return realPath
}

// createDiskImage creates a sparse disk image using truncate (same as vz library).
func createDiskImage(path string, sizeGB uint64) error {
	sizeBytes := int64(sizeGB) * 1024 * 1024 * 1024
	return createSparseDiskImageBytes(path, sizeBytes)
}

func createInstallDiskImage(path string, sizeGB uint64) error {
	sizeBytes := int64(sizeGB) * 1024 * 1024 * 1024
	return createInstallDiskImageBytes(path, sizeBytes)
}

func createInstallDiskImageBytes(path string, sizeBytes int64) error {
	if rawDisk {
		return createRawDiskImageBytes(path, sizeBytes)
	}
	return createSparseDiskImageBytes(path, sizeBytes)
}

func createSparseDiskImageBytes(path string, sizeBytes int64) error {
	if err := checkDiskSpace(filepath.Dir(path), sizeBytes); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil // Already exists
		}
		return err
	}
	defer f.Close()

	return f.Truncate(sizeBytes)
}

func createRawDiskImage(path string, sizeGB uint64) error {
	sizeBytes := int64(sizeGB) * 1024 * 1024 * 1024
	return createRawDiskImageBytes(path, sizeBytes)
}

func createRawDiskImageBytes(path string, sizeBytes int64) error {
	if err := checkDiskSpace(filepath.Dir(path), sizeBytes); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	if err := preallocateFile(f, sizeBytes); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func preallocateFile(f *os.File, sizeBytes int64) error {
	if sizeBytes < 0 {
		return fmt.Errorf("negative disk size %d", sizeBytes)
	}
	const blockSize = 8 * 1024 * 1024
	zero := make([]byte, blockSize)
	remaining := sizeBytes
	for remaining > 0 {
		n := int64(len(zero))
		if remaining < n {
			n = remaining
		}
		if _, err := f.Write(zero[:n]); err != nil {
			return fmt.Errorf("write zeros: %w", err)
		}
		remaining -= n
	}
	if err := f.Truncate(sizeBytes); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	return nil
}

func checkDiskSpace(dir string, needBytes int64) error {
	if needBytes <= 0 {
		return nil
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("statfs %s: %w", dir, err)
	}
	available := int64(stat.Bavail) * int64(stat.Bsize)
	if available < needBytes {
		return fmt.Errorf("insufficient disk space: need %s, have %s available", bytefmt.Size(needBytes), bytefmt.Size(available))
	}
	return nil
}

// savedTermios stores the original terminal settings for restoration
var savedTermios *unix.Termios

// setRawMode puts stdin into raw mode for direct serial console interaction.
// Returns a cleanup function to restore the original terminal settings.
func setRawMode() func() {
	fd := int(os.Stdin.Fd())

	// Get current terminal settings
	termios, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		fmt.Printf("  warning: could not get terminal settings: %v\n", err)
		return func() {}
	}

	// Save original settings
	savedTermios = new(unix.Termios)
	*savedTermios = *termios

	// Put into raw mode
	// Disable canonical mode (ICANON) and echo (ECHO)
	// Disable CR-NL mapping (ICRNL)
	termios.Iflag &^= unix.ICRNL
	termios.Lflag &^= unix.ICANON | unix.ECHO

	// Minimum chars = 1, timeout = 0
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, termios); err != nil {
		fmt.Printf("  warning: could not set raw mode: %v\n", err)
		return func() {}
	}

	// Return cleanup function
	return func() {
		if savedTermios != nil {
			unix.IoctlSetTermios(fd, unix.TIOCSETA, savedTermios)
			savedTermios = nil
		}
	}
}

// mainSigCh is the channel installed by setupSignalHandler. Exposed at
// package scope so subsystems (e.g. the Linux shell wrapper) can detach
// SIGINT via signal.Reset and later reclaim it via reclaimMainSignals.
// nil until setupSignalHandler runs.
var mainSigCh chan os.Signal

// reclaimMainSignals re-subscribes the main signal handler to the given
// signals. Call after a subsystem that took over a signal (via
// signal.Reset) has finished, to restore default cove behavior. Idempotent
// and a no-op if setupSignalHandler has not been called.
func reclaimMainSignals(signals ...os.Signal) {
	if mainSigCh == nil || len(signals) == 0 {
		return
	}
	signal.Notify(mainSigCh, signals...)
}

// setupSignalHandler sets up signal handling for graceful cleanup.
// SIGINT/SIGTERM: suspend and exit.
// SIGHUP: suspend, then re-exec the binary (picks up rebuilt binary, resumes from saved state).
func setupSignalHandler(cleanup func()) {
	sigCh := make(chan os.Signal, 1)
	mainSigCh = sigCh
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGUSR1)
	go func() {
		sig := <-sigCh
		reexec := sig == syscall.SIGHUP || sig == syscall.SIGUSR1
		if reexec {
			fmt.Printf("\nReceived %v, suspending VM for re-exec...\n", sig)
		} else {
			fmt.Printf("\nReceived %v, suspending VM...\n", sig)
		}
		done := make(chan struct{})
		go func() {
			cleanup()
			close(done)
		}()
		// Wait for cleanup or a second signal to force-quit.
		select {
		case <-done:
			if reexec {
				reexecSelf()
			}
			os.Exit(0)
		case <-sigCh:
			fmt.Println("\nForce quitting...")
			os.Exit(1)
		}
	}()
}

// reexecExitCode is a special exit code that signals the process wants to
// be re-executed. Used as fallback when fork+exec isn't possible.
const reexecExitCode = 75

// reexecSelf spawns a fresh copy of the process and exits.
// The VM state has already been saved to disk, so the new process will
// restore from it automatically.
//
// We use fork+exec (os/exec.Command) rather than syscall.Exec because
// the window server connection (Mach ports) survives exec but is stale,
// causing NSApplication to hang on the re-exec'd process. A fresh PID
// gets clean Mach port state.
func reexecSelf() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "re-exec: resolve executable: %v\n", err)
		os.Exit(reexecExitCode)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// If running under macgo, use the original executable path.
	if orig := os.Getenv("MACGO_ORIGINAL_EXECUTABLE"); orig != "" {
		exe = orig
	}

	fmt.Printf("Re-executing: %s %s\n", exe, strings.Join(os.Args[1:], " "))

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // new session, clean tty

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "re-exec: start failed: %v\n", err)
		os.Exit(reexecExitCode)
	}
	fmt.Printf("Started new process (PID %d), exiting.\n", cmd.Process.Pid)
	os.Exit(0)
}

// serialOutputFile holds the output file handle if writing to a file
var serialOutputFile *os.File

// createSerialConsoleConfig creates a serial console configuration based on the -serial flag.
// Options: 'stdout' (default), 'none' (disable), or a file path.
// Note: This relies on the global 'serialOutput' flag from main.go
func createSerialConsoleConfig() vz.VZSerialPortConfiguration {
	attachment, ok := createSerialPortAttachment()
	if !ok {
		return vz.VZSerialPortConfiguration{}
	}

	serialConfig := vz.NewVZVirtioConsoleDeviceSerialPortConfiguration()
	if serialConfig.ID == 0 {
		fmt.Println("  warning: could not create serial port configuration")
		return vz.VZSerialPortConfiguration{}
	}
	serialConfig.SetAttachment(&attachment.VZSerialPortAttachment)
	return vz.VZSerialPortConfigurationFromID(serialConfig.ID)
}

func createSerialPortAttachment() (vz.VZFileHandleSerialPortAttachment, bool) {
	if serialOutput == "none" {
		return vz.VZFileHandleSerialPortAttachment{}, false
	}

	var readFd, writeFd int

	readFd = int(os.Stdin.Fd())

	if serialOutput == "stdout" {
		writeFd = int(os.Stdout.Fd())
	} else {
		var err error
		serialOutputFile, err = os.OpenFile(serialOutput, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("  warning: could not open serial output file %s: %v\n", serialOutput, err)
			return vz.VZFileHandleSerialPortAttachment{}, false
		}
		writeFd = int(serialOutputFile.Fd())
		fmt.Printf("  Serial output will be written to: %s\n", serialOutput)
	}

	readHandle := foundation.NewFileHandleWithFileDescriptor(readFd)
	readHandle.Retain()
	writeHandle := foundation.NewFileHandleWithFileDescriptor(writeFd)
	writeHandle.Retain()

	attachment := vz.NewFileHandleSerialPortAttachmentWithFileHandleForReadingFileHandleForWriting(readHandle, writeHandle)
	if attachment.ID == 0 {
		fmt.Printf("  warning: could not create serial port attachment\n")
		return vz.VZFileHandleSerialPortAttachment{}, false
	}
	attachment.Retain()
	return attachment, true
}

// closeSerialOutputFile closes the serial output file if one was opened
func closeSerialOutputFile() {
	if serialOutputFile != nil {
		serialOutputFile.Close()
		serialOutputFile = nil
	}
}

// hintEntitlements wraps a Virtualization framework error with a hint about
// missing entitlements when the error looks like an XPC/service failure.
func hintEntitlements(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "catalog failed to load") ||
		strings.Contains(msg, "installation service") ||
		strings.Contains(msg, "unexpected error") {
		return fmt.Errorf("%w\n\n  This usually means the binary is missing the com.apple.security.virtualization entitlement.\n  Fix: codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove", err)
	}
	return err
}

// saveNSDataToFile saves NSData bytes to a file.
func saveNSDataToFile(dataID objc.ID, path string) error {
	data := foundation.NSDataFromID(dataID)
	length := data.Length()
	if length == 0 {
		return fmt.Errorf("empty data")
	}
	ptr := data.Bytes()
	bytes := unsafe.Slice((*byte)(ptr), length)
	return os.WriteFile(path, bytes, 0644)
}

// createNSDataFromBytes creates an NSData object from Go bytes.
func createNSDataFromBytes(data []byte) objc.ID {
	if len(data) == 0 {
		return 0
	}
	return foundation.GetNSDataClass().DataWithBytesLength(data).ID
}
