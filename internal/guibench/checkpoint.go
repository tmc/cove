package guibench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CheckpointFile is the canonical filename of the incremental outcome log
// inside a checkpoint directory.
const CheckpointFile = "outcomes.jsonl"

// Checkpoint is the incremental result store that lets a long scoring run
// resume after an interruption (design 047 §9 slice 2). Each scored [Outcome]
// is appended as one JSON line; a re-run loads the log and skips any (provider,
// task, run) cell already recorded, so a crash or a Ctrl-C costs at most the
// in-flight task, not the whole matrix.
//
// The zero value is not usable; construct with [OpenCheckpoint]. A Checkpoint
// is not safe for concurrent use: the runner appends from a single goroutine.
type Checkpoint struct {
	path     string
	w        io.WriteCloser
	outcomes []Outcome
}

// OpenCheckpoint opens (or creates) the checkpoint log in dir, loading any
// outcomes already recorded so the runner can skip them. The directory is
// created if absent. A malformed line in an existing log is a hard error rather
// than a silent skip, so a corrupt checkpoint never causes a re-run to
// double-count or silently lose results.
func OpenCheckpoint(dir string) (*Checkpoint, error) {
	if dir == "" {
		return nil, fmt.Errorf("checkpoint: dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("checkpoint: create dir: %w", err)
	}
	path := filepath.Join(dir, CheckpointFile)
	prior, err := loadOutcomes(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: open %s: %w", path, err)
	}
	return &Checkpoint{path: path, w: f, outcomes: prior}, nil
}

// Outcomes returns the outcomes loaded from the log plus any appended this
// session, in record order. The runner uses it to build the resume set.
func (c *Checkpoint) Outcomes() []Outcome {
	if c == nil {
		return nil
	}
	out := make([]Outcome, len(c.outcomes))
	copy(out, c.outcomes)
	return out
}

// Append writes one outcome to the log and records it in memory. It flushes to
// disk before returning, so an outcome survives a crash on the very next task.
func (c *Checkpoint) Append(o Outcome) error {
	if c == nil {
		return nil
	}
	data, err := json.Marshal(o)
	if err != nil {
		return fmt.Errorf("checkpoint: marshal outcome: %w", err)
	}
	if _, err := c.w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("checkpoint: append: %w", err)
	}
	if f, ok := c.w.(*os.File); ok {
		if err := f.Sync(); err != nil {
			return fmt.Errorf("checkpoint: sync: %w", err)
		}
	}
	c.outcomes = append(c.outcomes, o)
	return nil
}

// Has reports whether the (provider, task, run) cell is already recorded, so a
// caller can decide to skip it without rebuilding the resume map.
func (c *Checkpoint) Has(provider, taskID string, run int) bool {
	if c == nil {
		return false
	}
	for _, o := range c.outcomes {
		if o.Provider == provider && o.TaskID == taskID && o.Run == run {
			return true
		}
	}
	return false
}

// Close closes the underlying log file.
func (c *Checkpoint) Close() error {
	if c == nil || c.w == nil {
		return nil
	}
	return c.w.Close()
}

// loadOutcomes reads every outcome from an existing log. A missing file is not
// an error (a fresh run); a malformed line is.
func loadOutcomes(path string) ([]Outcome, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint: read %s: %w", path, err)
	}
	defer f.Close()
	var out []Outcome
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Bytes()
		if len(text) == 0 {
			continue
		}
		var o Outcome
		if err := json.Unmarshal(text, &o); err != nil {
			return nil, fmt.Errorf("checkpoint: %s line %d: %w", path, line, err)
		}
		out = append(out, o)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("checkpoint: scan %s: %w", path, err)
	}
	return out, nil
}
