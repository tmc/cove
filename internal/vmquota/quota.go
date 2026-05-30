// Package vmquota persists and applies per-VM resource quotas.
package vmquota

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const FileName = "quotas.json"

// ErrQuotaExceeded reports that a request exceeds a configured quota.
var ErrQuotaExceeded = errors.New("quota exceeded")

// ErrAPFSQuotaUnsupported reports a host diskutil without directory quotas.
var ErrAPFSQuotaUnsupported = errors.New("apfs directory quotas unsupported")

type Quota struct {
	CPUs     uint   `json:"cpus,omitempty"`
	MemoryGB uint64 `json:"memory_gb,omitempty"`
	DiskGB   uint64 `json:"disk_gb,omitempty"`
}

type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Check reports whether request fits within q.
func (q Quota) Check(request Quota) error {
	if q.CPUs > 0 && request.CPUs > q.CPUs {
		return fmt.Errorf("%w: cpus %d > %d", ErrQuotaExceeded, request.CPUs, q.CPUs)
	}
	if q.MemoryGB > 0 && request.MemoryGB > q.MemoryGB {
		return fmt.Errorf("%w: memory %d GB > %d GB", ErrQuotaExceeded, request.MemoryGB, q.MemoryGB)
	}
	if q.DiskGB > 0 && request.DiskGB > q.DiskGB {
		return fmt.Errorf("%w: disk %d GB > %d GB", ErrQuotaExceeded, request.DiskGB, q.DiskGB)
	}
	return nil
}

func Load(vmDir string) (Quota, error) {
	data, err := os.ReadFile(filepath.Join(vmDir, FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Quota{}, nil
		}
		return Quota{}, fmt.Errorf("read quota: %w", err)
	}
	var q Quota
	if err := json.Unmarshal(data, &q); err != nil {
		return Quota{}, fmt.Errorf("parse quota: %w", err)
	}
	return q, nil
}

func Save(vmDir string, q Quota) error {
	data, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal quota: %w", err)
	}
	path := filepath.Join(vmDir, FileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write quota: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename quota: %w", err)
	}
	return nil
}

// macOS 26 (Darwin 25) removed the "diskutil apfs setQuota" verb. APFSQuotaSupported
// reports whether the host's diskutil recognizes it, so callers can skip the attempt
// (and its noisy "did not recognize" output) on hosts that dropped it.
//
// Support is discovered by asking diskutil itself rather than inferring from the OS
// version: "diskutil apfs" with no verb prints the list of verbs it accepts, and we
// check whether "setQuota" is among them. This is version-agnostic — it stays correct
// if a future macOS re-adds the verb, or an older one lacks it. The Darwin-release
// heuristic remains only as a fallback for when diskutil cannot be consulted.
var (
	apfsQuotaSupportedOnce sync.Once
	apfsQuotaSupportedVal  bool
	// apfsQuotaSupported is the gate ApplyAPFSQuota consults; overridable in tests.
	apfsQuotaSupported = APFSQuotaSupported
)

// APFSQuotaSupported reports whether this host supports "diskutil apfs setQuota".
// The result is probed once per process and cached.
func APFSQuotaSupported() bool {
	apfsQuotaSupportedOnce.Do(func() {
		apfsQuotaSupportedVal = probeAPFSQuotaSupported(execRunner{})
	})
	return apfsQuotaSupportedVal
}

// probeAPFSQuotaSupported discovers setQuota support from the host's diskutil. It
// runs "diskutil apfs" (no verb), which prints the supported verb list, and reports
// whether setQuota appears. If diskutil cannot be consulted (missing binary, empty or
// unparseable output), it falls back to the Darwin-release heuristic so callers still
// get a reasonable answer; a wrong guess there is caught at apply time by the
// ErrAPFSQuotaUnsupported path.
func probeAPFSQuotaSupported(runner Runner) bool {
	out, err := runner.Run("diskutil", "apfs")
	if listed, ok := apfsVerbListed(out, "setQuota"); ok {
		return listed
	}
	// Could not read a usable verb list (err and/or unrecognized output); guess from
	// the OS version as a last resort.
	_ = err
	release, rerr := unix.Sysctl("kern.osrelease")
	if rerr != nil {
		return true
	}
	return apfsQuotaSupportedForRelease(release)
}

// apfsVerbListed reports whether verb appears in the output of "diskutil apfs"
// (the verb-listing help text). ok is false when out does not look like a verb
// list at all, so the caller can fall back to another signal. Matching is
// case-insensitive because diskutil's own casing has varied across releases.
func apfsVerbListed(out []byte, verb string) (listed, ok bool) {
	text := strings.ToLower(string(out))
	// Sanity-check that this is the verb-listing help and not, say, an error: every
	// known diskutil prints these alongside the verb table.
	if !strings.Contains(text, "verb") || !strings.Contains(text, "list") {
		return false, false
	}
	return strings.Contains(text, strings.ToLower(verb)), true
}

// apfsQuotaSupportedForRelease reports whether the given Darwin kernel release
// (e.g. "24.6.0") is expected to support "diskutil apfs setQuota". It is only a
// fallback for when diskutil's verb list cannot be read; discovery via
// apfsVerbListed is preferred. Darwin 24 is macOS 15 (Sequoia, last to ship the
// verb); Darwin 25 is macOS 26, which removed it. An unparseable release is treated
// as supported so callers fall back to probing diskutil at apply time.
func apfsQuotaSupportedForRelease(release string) bool {
	major := release
	if i := strings.IndexByte(major, '.'); i >= 0 {
		major = major[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil {
		return true
	}
	return n < 25
}

func ApplyAPFSQuota(vmDir string, gb uint64) error {
	return ApplyAPFSQuotaWithRunner(vmDir, gb, execRunner{})
}

func ApplyAPFSQuotaWithRunner(vmDir string, gb uint64, runner Runner) error {
	if strings.TrimSpace(vmDir) == "" {
		return fmt.Errorf("vm directory required")
	}
	if gb == 0 {
		return fmt.Errorf("disk quota must be greater than 0")
	}
	if runner == nil {
		return fmt.Errorf("runner required")
	}
	if !apfsQuotaSupported() {
		// Host dropped the setQuota verb; treat as a successful no-op so callers
		// persist DiskGB for daemon enforcement without a spurious failure.
		return nil
	}
	out, err := runner.Run("diskutil", "apfs", "setQuota", vmDir, fmt.Sprintf("%dg", gb))
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, `did not recognize APFS verb "setQuota"`) {
			return fmt.Errorf("%w: %s", ErrAPFSQuotaUnsupported, msg)
		}
		if msg == "" {
			return fmt.Errorf("apply apfs quota: %w", err)
		}
		return fmt.Errorf("apply apfs quota: %w: %s", err, msg)
	}
	return nil
}
