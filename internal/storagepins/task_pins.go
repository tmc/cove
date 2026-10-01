package storagepins

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxPinFileBytes = 4 << 20
const maxTaskPins = 4096

// TaskOwner identifies one task attempt and its owning process generation.
type TaskOwner struct {
	RunID      string `json:"run_id"`
	AttemptID  string `json:"attempt_id"`
	Generation string `json:"generation"`
	PID        int    `json:"pid"`
	StartedAt  string `json:"started_at"`
}

// DirectoryIdentity identifies a directory independently of its registered name.
type DirectoryIdentity struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

// TaskPin protects an object for a specific task owner and directory identity.
type TaskPin struct {
	Category string            `json:"category"`
	ID       string            `json:"id"`
	AddedAt  time.Time         `json:"added_at"`
	Owner    TaskOwner         `json:"owner"`
	Identity DirectoryIdentity `json:"identity"`
}

// Ref returns the canonical object reference.
func (p TaskPin) Ref() string { return p.Category + ":" + p.ID }

func taskPinKey(category, id, generation string) string {
	return category + ":" + id + "/" + generation
}

func validateTaskOwner(owner TaskOwner) error {
	for _, value := range []string{owner.RunID, owner.AttemptID} {
		if value == "" || len(value) > 256 || strings.ContainsAny(value, " \t\r\n\x00/\\") {
			return fmt.Errorf("invalid task pin attempt identity")
		}
	}
	if len(owner.Generation) != 32 || owner.PID <= 0 || len(owner.StartedAt) > 64 {
		return fmt.Errorf("invalid task pin owner")
	}
	generation, err := hex.DecodeString(owner.Generation)
	if err != nil || hex.EncodeToString(generation) != owner.Generation {
		return fmt.Errorf("invalid task pin generation")
	}
	started, err := time.Parse(time.RFC3339Nano, owner.StartedAt)
	if err != nil || started.IsZero() {
		return fmt.Errorf("invalid task pin owner start time")
	}
	return nil
}

func validateTaskIdentity(identity DirectoryIdentity) error {
	if identity.Path == "" || len(identity.Path) > 4096 || !filepath.IsAbs(identity.Path) || filepath.Clean(identity.Path) != identity.Path || identity.Path == string(filepath.Separator) || strings.ContainsRune(identity.Path, 0) || identity.Inode == 0 {
		return fmt.Errorf("invalid task pin directory identity")
	}
	return nil
}

func validateTaskPin(pin TaskPin) error {
	if len(pin.ID) > 256 || strings.ContainsRune(pin.ID, 0) {
		return fmt.Errorf("task pin object id exceeds bounds")
	}
	if _, _, err := ParseRef(pin.Ref()); err != nil {
		return err
	}
	if pin.Category != "vm" && pin.Category != "run" {
		return fmt.Errorf("task pins require a VM or run directory")
	}
	if pin.AddedAt.IsZero() {
		return fmt.Errorf("task pin addition time required")
	}
	if err := validateTaskOwner(pin.Owner); err != nil {
		return err
	}
	return validateTaskIdentity(pin.Identity)
}

// AddTask records a generation-owned pin without changing operator pins.
// Repeating the same owner and identity preserves the original addition time.
func (f *File) AddTask(pin TaskPin) error {
	if err := validateTaskPin(pin); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taskPins == nil {
		f.taskPins = map[string]TaskPin{}
	}
	key := taskPinKey(pin.Category, pin.ID, pin.Owner.Generation)
	if previous, exists := f.taskPins[key]; exists {
		if previous.Owner != pin.Owner || previous.Identity != pin.Identity {
			return fmt.Errorf("task pin owner or identity differs")
		}
		return nil
	}
	if len(f.taskPins) >= maxTaskPins {
		return fmt.Errorf("task pin count exceeds bounds")
	}
	pin.AddedAt = pin.AddedAt.UTC()
	f.taskPins[key] = pin
	return nil
}

// RemoveTask removes only a matching task-generation pin. It does not infer
// cleanup authority or process death; callers must establish their disposition.
func (f *File) RemoveTask(category, id string, owner TaskOwner, identity DirectoryIdentity) (bool, error) {
	if len(id) > 256 || (category != "vm" && category != "run") {
		return false, fmt.Errorf("invalid task pin object")
	}
	if _, _, err := ParseRef(category + ":" + id); err != nil {
		return false, err
	}
	if err := validateTaskOwner(owner); err != nil {
		return false, err
	}
	if err := validateTaskIdentity(identity); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := taskPinKey(category, id, owner.Generation)
	pin, exists := f.taskPins[key]
	if !exists {
		for _, other := range f.taskPins {
			if other.Ref() == category+":"+id {
				return false, fmt.Errorf("task pin generation differs")
			}
		}
		return false, nil
	}
	if pin.Owner != owner || pin.Identity != identity {
		return false, fmt.Errorf("task pin owner or identity differs")
	}
	delete(f.taskPins, key)
	return true, nil
}

// TaskPins returns task pins sorted by object reference and generation.
func (f *File) TaskPins() []TaskPin {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]TaskPin, 0, len(f.taskPins))
	for _, pin := range f.taskPins {
		out = append(out, pin)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref() != out[j].Ref() {
			return out[i].Ref() < out[j].Ref()
		}
		return out[i].Owner.Generation < out[j].Owner.Generation
	})
	return out
}

// IsOperatorPinned reports whether an operator independently pinned the object.
func (f *File) IsOperatorPinned(category, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, exists := f.pins[category+":"+id]
	return exists
}
