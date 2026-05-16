package vmpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const fileName = "policy.json"

// Policy describes per-VM lifecycle stop thresholds.
type Policy struct {
	IdleTimeout time.Duration
	MaxAge      time.Duration
	RunBudget   int
}

// Default returns the empty policy.
func Default() Policy {
	return Policy{}
}

// Empty reports whether no thresholds are set.
func (p Policy) Empty() bool {
	return p.IdleTimeout <= 0 && p.MaxAge <= 0 && p.RunBudget <= 0
}

// Merge returns a copy of p with non-zero fields from update applied.
func (p Policy) Merge(update Policy) Policy {
	if update.IdleTimeout > 0 {
		p.IdleTimeout = update.IdleTimeout
	}
	if update.MaxAge > 0 {
		p.MaxAge = update.MaxAge
	}
	if update.RunBudget > 0 {
		p.RunBudget = update.RunBudget
	}
	return p
}

// Validate rejects negative thresholds and invalid budgets.
func (p Policy) Validate() error {
	if p.IdleTimeout < 0 {
		return fmt.Errorf("idle timeout must be non-negative")
	}
	if p.MaxAge < 0 {
		return fmt.Errorf("max age must be non-negative")
	}
	if p.RunBudget < 0 {
		return fmt.Errorf("run budget must be non-negative")
	}
	return nil
}

// Path returns the on-disk policy path for a VM directory.
func Path(vmDir string) string {
	return filepath.Join(vmDir, fileName)
}

// Load reads the policy file from vmDir.
func Load(vmDir string) (Policy, error) {
	data, err := os.ReadFile(Path(vmDir))
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Policy{}, fmt.Errorf("read vm policy: %w", err)
	}
	var onDisk diskPolicy
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return Policy{}, fmt.Errorf("parse vm policy: %w", err)
	}
	p, err := onDisk.toPolicy()
	if err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Save writes policy to vmDir atomically.
func Save(vmDir string, policy Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(policy.toDisk(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vm policy: %w", err)
	}
	path := Path(vmDir)
	tmpPath := path + ".tmp"
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create vm policy dir: %w", err)
	}
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write vm policy: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename vm policy: %w", err)
	}
	return nil
}

// Clear removes the persisted policy file.
func Clear(vmDir string) error {
	if err := os.Remove(Path(vmDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove vm policy: %w", err)
	}
	return nil
}

type diskPolicy struct {
	IdleTimeout string `json:"idleTimeout,omitempty"`
	MaxAge      string `json:"maxAge,omitempty"`
	RunBudget   int    `json:"runBudget,omitempty"`
}

func (p Policy) toDisk() diskPolicy {
	return diskPolicy{
		IdleTimeout: durationString(p.IdleTimeout),
		MaxAge:      durationString(p.MaxAge),
		RunBudget:   p.RunBudget,
	}
}

func (p diskPolicy) toPolicy() (Policy, error) {
	out := Policy{RunBudget: p.RunBudget}
	if p.IdleTimeout != "" {
		d, err := time.ParseDuration(p.IdleTimeout)
		if err != nil {
			return Policy{}, fmt.Errorf("parse idle timeout: %w", err)
		}
		out.IdleTimeout = d
	}
	if p.MaxAge != "" {
		d, err := time.ParseDuration(p.MaxAge)
		if err != nil {
			return Policy{}, fmt.Errorf("parse max age: %w", err)
		}
		out.MaxAge = d
	}
	if out.RunBudget < 0 {
		return Policy{}, fmt.Errorf("run budget must be non-negative")
	}
	if err := out.Validate(); err != nil {
		return Policy{}, err
	}
	return out, nil
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}
