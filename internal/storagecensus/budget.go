package storagecensus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// BudgetFilename is the basename of the persisted budget under ~/.vz/.
const BudgetFilename = "storage-budget.json"

// Budget is the operator's storage target. Phase 2 of design 040.
//
// TargetBytes is the soft watermark. WarnPct and HardPct are tripwire
// percentages of the target (0–100). The zero value means "no budget".
type Budget struct {
	TargetBytes int64 `json:"target_bytes"`
	WarnPct     int   `json:"warn_pct"`
	HardPct     int   `json:"hard_pct"`
}

// IsSet reports whether the budget is configured. Zero value is "unset".
func (b Budget) IsSet() bool { return b.TargetBytes > 0 }

// WarnBytes returns the byte threshold at which the budget enters warning
// state. Zero when the budget is unset or WarnPct is zero.
func (b Budget) WarnBytes() int64 {
	if !b.IsSet() || b.WarnPct <= 0 {
		return 0
	}
	return b.TargetBytes / 100 * int64(b.WarnPct)
}

// HardBytes returns the byte threshold at which the budget enters hard state.
// Zero when the budget is unset or HardPct is zero.
func (b Budget) HardBytes() int64 {
	if !b.IsSet() || b.HardPct <= 0 {
		return 0
	}
	return b.TargetBytes / 100 * int64(b.HardPct)
}

// Validate returns nil if b is a usable budget. The zero value is valid:
// callers use IsSet to distinguish "unset" from "set and valid".
func (b Budget) Validate() error {
	if b.TargetBytes < 0 {
		return fmt.Errorf("target_bytes must be non-negative")
	}
	if b.WarnPct < 0 || b.WarnPct > 100 {
		return fmt.Errorf("warn_pct must be in [0,100]")
	}
	if b.HardPct < 0 || b.HardPct > 100 {
		return fmt.Errorf("hard_pct must be in [0,100]")
	}
	if b.WarnPct > 0 && b.HardPct > 0 && b.WarnPct > b.HardPct {
		return fmt.Errorf("warn_pct (%d) must not exceed hard_pct (%d)", b.WarnPct, b.HardPct)
	}
	return nil
}

// LoadBudget reads ~/.vz/storage-budget.json from the cove root. A missing
// file returns the zero Budget and a nil error so callers can treat
// "no budget" the same as "no enforcement requested".
func LoadBudget(root string) (Budget, error) {
	path := filepath.Join(root, BudgetFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Budget{}, nil
		}
		return Budget{}, fmt.Errorf("read storage budget: %w", err)
	}
	var b Budget
	if err := json.Unmarshal(data, &b); err != nil {
		return Budget{}, fmt.Errorf("parse storage budget: %w", err)
	}
	if err := b.Validate(); err != nil {
		return Budget{}, fmt.Errorf("storage budget: %w", err)
	}
	return b, nil
}

// SaveBudget writes b to ~/.vz/storage-budget.json. The cove root is
// created if missing. SaveBudget rejects an invalid budget without
// touching the file.
func SaveBudget(root string, b Budget) error {
	if err := b.Validate(); err != nil {
		return fmt.Errorf("storage budget: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("ensure cove root: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encode storage budget: %w", err)
	}
	path := filepath.Join(root, BudgetFilename)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write storage budget: %w", err)
	}
	return nil
}

// EncodeBudgetJSON writes b as indented JSON to w.
func EncodeBudgetJSON(w io.Writer, b Budget) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

// ClearBudget removes ~/.vz/storage-budget.json. A missing file is not an
// error; the post-condition is "no budget on disk".
func ClearBudget(root string) error {
	path := filepath.Join(root, BudgetFilename)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear storage budget: %w", err)
	}
	return nil
}
