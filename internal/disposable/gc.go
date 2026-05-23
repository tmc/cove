package disposable

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const CloneStampFormat = "20060102-150405"

type Clone struct {
	Name      string
	Path      string
	Source    string
	CreatedAt time.Time
}

type GCOptions struct {
	BaseDir   string
	OlderThan time.Duration
	DryRun    bool
	Now       func() time.Time
	IsActive  func(string) bool
	RemoveAll func(string) error
}

type GCResult struct {
	Scanned      int
	Candidates   int
	SkippedAlive int
	Removed      int
	Paths        []string
}

func CloneName(base string, now time.Time) string {
	base = BaseName(base)
	return fmt.Sprintf("%s-d-%s", base, now.Format(CloneStampFormat))
}

func ParseCloneName(name string) (base string, createdAt time.Time, ok bool) {
	idx := strings.LastIndex(name, "-d-")
	if idx <= 0 {
		return "", time.Time{}, false
	}
	stamp := name[idx+3:]
	if len(stamp) != len(CloneStampFormat) {
		return "", time.Time{}, false
	}
	createdAt, err := time.ParseInLocation(CloneStampFormat, stamp, time.Local)
	if err != nil {
		return "", time.Time{}, false
	}
	base = strings.TrimSpace(name[:idx])
	if base == "" {
		base = "vm"
	}
	return base, createdAt, true
}

func GC(opts GCOptions) (GCResult, error) {
	if opts.BaseDir == "" {
		return GCResult{}, fmt.Errorf("disposable gc: base dir required")
	}
	if opts.IsActive == nil {
		return GCResult{}, fmt.Errorf("disposable gc: active check required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	removeAll := opts.RemoveAll
	if removeAll == nil {
		removeAll = os.RemoveAll
	}

	entries, err := os.ReadDir(opts.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return GCResult{}, nil
		}
		return GCResult{}, fmt.Errorf("read vm base dir: %w", err)
	}

	var result GCResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		_, createdAt, ok := ParseCloneName(name)
		if !ok {
			continue
		}
		result.Scanned++
		path := filepath.Join(opts.BaseDir, name)
		if opts.IsActive(path) {
			result.SkippedAlive++
			continue
		}
		if opts.OlderThan > 0 && now().Sub(createdAt) < opts.OlderThan {
			continue
		}
		result.Candidates++
		result.Paths = append(result.Paths, path)
		if opts.DryRun {
			continue
		}
		if err := removeAll(path); err != nil {
			return result, fmt.Errorf("remove disposable clone %s: %w", path, err)
		}
		result.Removed++
	}

	return result, nil
}

func BaseName(base string) string {
	base = strings.TrimSpace(filepath.Base(base))
	switch base {
	case "", ".", "..", string(filepath.Separator):
		return "vm"
	default:
		return base
	}
}
