// Package vmquota persists and applies per-VM resource quotas.
package vmquota

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const FileName = "quotas.json"

// ErrQuotaExceeded reports that a request exceeds a configured quota.
var ErrQuotaExceeded = errors.New("quota exceeded")

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

// PositiveValue validates a signed quota value before storing it.
func PositiveValue(name string, n int64) (uint64, error) {
	if strings.TrimSpace(name) == "" {
		name = "quota"
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s quota must be greater than 0", name)
	}
	return uint64(n), nil
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
	out, err := runner.Run("diskutil", "apfs", "setQuota", vmDir, fmt.Sprintf("%dg", gb))
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("apply apfs quota: %w", err)
		}
		return fmt.Errorf("apply apfs quota: %w: %s", err, msg)
	}
	return nil
}
