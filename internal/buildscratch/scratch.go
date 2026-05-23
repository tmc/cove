package buildscratch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Scratch struct {
	ID       string
	Dir      string
	PIDPath  string
	DiskPath string
	LogPath  string
	Created  time.Time
}

type Meta struct {
	ID               string    `json:"id"`
	PID              int       `json:"pid"`
	PlanDigest       string    `json:"plan_digest,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	KeepIntermediate bool      `json:"keep_intermediate"`
}

// Entry describes one build scratch directory considered by Prune.
type Entry struct {
	Dir     string
	Age     time.Duration
	Bytes   int64
	Reason  string
	Removed bool
}

// Report is the result of one Prune call.
type Report struct {
	Root         string
	OlderThan    time.Duration
	SanityFloor  time.Duration
	Apply        bool
	Entries      []Entry
	BytesRemoved int64
	BytesKept    int64
}

const PruneSanityFloor = time.Hour

func Prune(root string, olderThan time.Duration, apply bool, isLive func(int) bool, now func() time.Time) (Report, error) {
	if root == "" {
		return Report{}, fmt.Errorf("prune build scratch: root required")
	}
	if isLive == nil {
		return Report{}, fmt.Errorf("prune build scratch: live process check required")
	}
	if now == nil {
		now = time.Now
	}
	floor := PruneSanityFloor
	if olderThan < floor {
		olderThan = floor
	}
	rep := Report{
		Root:        root,
		OlderThan:   olderThan,
		SanityFloor: floor,
		Apply:       apply,
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return rep, nil
	}
	if err != nil {
		return rep, fmt.Errorf("read build scratch: %w", err)
	}
	cutoff := now().Add(-olderThan)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			rep.Entries = append(rep.Entries, Entry{Dir: dir, Reason: "skipped-error"})
			continue
		}
		bytes := DirBytes(dir)
		age := now().Sub(info.ModTime())
		ent := Entry{Dir: dir, Age: age, Bytes: bytes}
		if info.ModTime().After(cutoff) {
			ent.Reason = "too-young"
			rep.BytesKept += bytes
			rep.Entries = append(rep.Entries, ent)
			continue
		}
		if pid, ok := ReadPID(filepath.Join(dir, "build.pid")); ok && isLive(pid) {
			ent.Reason = "live-pid"
			rep.BytesKept += bytes
			rep.Entries = append(rep.Entries, ent)
			continue
		}
		if !apply {
			ent.Reason = "candidate"
			rep.BytesRemoved += bytes
			rep.Entries = append(rep.Entries, ent)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			ent.Reason = "skipped-error"
			rep.BytesKept += bytes
			rep.Entries = append(rep.Entries, ent)
			continue
		}
		ent.Reason = "removed"
		ent.Removed = true
		rep.BytesRemoved += bytes
		rep.Entries = append(rep.Entries, ent)
	}
	return rep, nil
}

func DirBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func GC(root string, isLive func(int) bool) error {
	if root == "" {
		return nil
	}
	if isLive == nil {
		return fmt.Errorf("gc build scratch: live process check required")
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read build scratch: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		pid, ok := ReadPID(filepath.Join(dir, "build.pid"))
		if !ok {
			continue
		}
		if isLive != nil && isLive(pid) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove stale build scratch %s: %w", dir, err)
		}
	}
	return nil
}

func ReadPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func NewID(t time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("build scratch id: %w", err)
	}
	return t.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}
