package guibench

import (
	"fmt"
	"strings"
)

// FlushKind names a pre-read flush that settles async OS state before a getter
// reads it (design 047 §7). macOS preferences go through cfprefsd and app
// SQLite stores use a write-ahead log, so a getter that reads the backing file
// too early sees stale data — the single most likely source of false negatives
// (OSWorld's largest verifier-bug class was this async-vs-sync timing).
type FlushKind string

const (
	// FlushCfprefsd kills cfprefsd so a subsequent direct .plist read sees the
	// settled value. Prefer the defaults getter (which reads the live value
	// through cfprefsd) over flush-then-parse where possible.
	FlushCfprefsd FlushKind = "cfprefsd"
	// FlushWAL checkpoints a SQLite write-ahead log into the main db so an
	// offline read of the .db file is current. The sqlite getter checkpoints
	// inline; this helper is for tasks that read a db a different way.
	FlushWAL FlushKind = "wal"
)

// Flush runs the pre-read flush named by kind against the guest through p. For
// FlushWAL, path is the SQLite db to checkpoint; for FlushCfprefsd, path is
// ignored. It returns an error if the flush command fails, so a caller never
// proceeds to read stale state silently.
func Flush(p Probe, kind FlushKind, path string) error {
	switch kind {
	case FlushCfprefsd:
		return flushCfprefsd(p)
	case FlushWAL:
		if path == "" {
			return fmt.Errorf("flush wal: path is empty")
		}
		return flushWAL(p, path)
	default:
		return fmt.Errorf("flush: unknown kind %q", kind)
	}
}

// flushCfprefsd kills cfprefsd so the next direct plist read is current. A
// `killall cfprefsd` that finds no process exits nonzero with "No matching
// processes"; that is benign and not treated as an error.
func flushCfprefsd(p Probe) error {
	exit, _, stderr, err := p.Exec([]string{"killall", "cfprefsd"}, nil, "")
	if err != nil {
		return fmt.Errorf("flush cfprefsd: %w", err)
	}
	if exit != 0 && !strings.Contains(stderr, "No matching processes") {
		return fmt.Errorf("flush cfprefsd: killall exited %d: %s", exit, strings.TrimSpace(stderr))
	}
	return nil
}

// flushWAL checkpoints the SQLite write-ahead log for the db at path so a
// subsequent file read reflects committed writes.
func flushWAL(p Probe, path string) error {
	exit, _, stderr, err := p.Exec([]string{"sqlite3", path, "PRAGMA wal_checkpoint(FULL);"}, nil, "")
	if err != nil {
		return fmt.Errorf("flush wal: %w", err)
	}
	if exit != 0 {
		return fmt.Errorf("flush wal: checkpoint %s exited %d: %s", path, exit, strings.TrimSpace(stderr))
	}
	return nil
}
