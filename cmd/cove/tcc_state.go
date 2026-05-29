// tcc_state.go - Host TCC pre-auth state file.
//
// cove triggers macOS Apple Events when it runs osascript-backed
// helpers on the host (System Events password dialogs from
// provision_cli.go, UTM discovery from utm.go). The first invocation
// of each shows a TCC consent dialog. Phase 2 of the TCC pre-auth work
// records, in ~/.vz/runtime/tcc.json, which AE services the user has
// already pre-flighted so cove can skip the prompt next time and warn
// when a prompt is still pending.
//
// The schema is intentionally small. The zero value of TCCState is a
// usable empty state.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TCCStateVersion is the on-disk schema version.
const TCCStateVersion = 1

// TCCResult is the outcome of a pre-flight attempt.
type TCCResult string

const (
	TCCResultUnknown TCCResult = ""
	TCCResultGranted TCCResult = "granted"
	TCCResultDenied  TCCResult = "denied"
	TCCResultSkipped TCCResult = "skipped"
)

// TCCEntry records a single AE service pre-flight outcome.
type TCCEntry struct {
	PreflightedAt time.Time `json:"preflighted_at"`
	Result        TCCResult `json:"result"`
}

// TCCState is the on-disk pre-auth state file. Host holds entries
// keyed by service id (e.g. "system_events", "utm").
type TCCState struct {
	Version int                 `json:"version"`
	Host    map[string]TCCEntry `json:"host,omitempty"`
}

// DefaultTCCStatePath returns the canonical state file path.
func DefaultTCCStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".vz", "runtime", "tcc.json"), nil
}

// LoadTCCState reads state from path. A missing file returns an empty
// state with no error so first-run callers can proceed.
func LoadTCCState(path string) (*TCCState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TCCState{Version: TCCStateVersion}, nil
		}
		return nil, fmt.Errorf("read tcc state %s: %w", path, err)
	}
	var s TCCState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse tcc state %s: %w", path, err)
	}
	if s.Version == 0 {
		s.Version = TCCStateVersion
	}
	return &s, nil
}

// SaveTCCState writes state to path, creating parent directories as
// needed. The write is atomic via rename.
func SaveTCCState(path string, s *TCCState) error {
	if s == nil {
		return fmt.Errorf("save tcc state: nil state")
	}
	if s.Version == 0 {
		s.Version = TCCStateVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create tcc state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tcc state: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write tcc state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename tcc state: %w", err)
	}
	return nil
}

// SetHostEntry records the outcome of a host AE service pre-flight.
// It allocates the host map on first use.
func (s *TCCState) SetHostEntry(service string, result TCCResult, now time.Time) {
	if s.Host == nil {
		s.Host = map[string]TCCEntry{}
	}
	s.Host[service] = TCCEntry{
		PreflightedAt: now.UTC(),
		Result:        result,
	}
}

// HostEntry returns the recorded entry for service, or zero value if
// none has been recorded.
func (s *TCCState) HostEntry(service string) (TCCEntry, bool) {
	if s == nil || s.Host == nil {
		return TCCEntry{}, false
	}
	e, ok := s.Host[service]
	return e, ok
}
