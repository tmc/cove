package runs

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const SchemaVersion = 1
const MaxArtifactBytes int64 = 32 << 20
const MaxRecordBytes = 1 << 20
const maxEventBytes = 64 << 20

// Task records declared provenance, never input or secret values. A missing
// digest or capability means unknown, not a verified image or guest state.
type Task struct {
	Provider     string   `json:"provider,omitempty"`
	Kind         string   `json:"kind"`
	ImageDigest  string   `json:"image_digest,omitempty"`
	PlanDigest   string   `json:"plan_digest,omitempty"`
	SourceDigest string   `json:"source_digest,omitempty"`
	Inputs       []Input  `json:"inputs,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	GuestRoute   string   `json:"guest_route,omitempty"`
	Backend      string   `json:"backend,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Retention    string   `json:"retention,omitempty"`
}

// Input identifies an input without storing its value. Secret inputs carry
// only their name and Secret=true, not a digest vulnerable to guessing.
type Input struct {
	Name   string `json:"name"`
	Digest string `json:"digest,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// Manifest extends the original run manifest without renaming its fields.
type Manifest struct {
	SchemaVersion int        `json:"schema_version,omitempty"`
	RunID         string     `json:"run_id"`
	AttemptID     string     `json:"attempt_id,omitempty"`
	VMName        string     `json:"vm_name"`
	ForkFrom      string     `json:"fork_from"`
	StartedAt     string     `json:"started_at"`
	EndedAt       string     `json:"ended_at,omitempty"`
	ExitStatus    string     `json:"exit_status,omitempty"`
	Outcome       string     `json:"outcome,omitempty"`
	PrimaryError  string     `json:"primary_error,omitempty"`
	CleanupErrors []string   `json:"cleanup_errors,omitempty"`
	CaptureErrors []string   `json:"capture_errors,omitempty"`
	Task          *Task      `json:"task,omitempty"`
	Artifacts     []Artifact `json:"artifacts,omitempty"`
}

// TaskEvent has per-attempt writer order independent of wall-clock order.
type TaskEvent struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id"`
	AttemptID     string         `json:"attempt_id"`
	Sequence      uint64         `json:"sequence"`
	Kind          string         `json:"kind"`
	StepID        string         `json:"step_id,omitempty"`
	Timestamp     string         `json:"ts"`
	DurationMS    int64          `json:"duration_ms,omitempty"`
	Status        string         `json:"status,omitempty"`
	Payload       map[string]any `json:"payload,omitempty"`
	ArtifactRefs  []string       `json:"artifact_refs,omitempty"`
}

// Artifact records diagnostic availability. Present artifacts are bounded
// relative regular files with a SHA-256 digest; unavailable files have a reason.
type Artifact struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	Digest      string `json:"digest,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// Record is the read-only task view of a bundle. Incomplete means there is no
// terminal manifest; TruncatedEvents marks only an interrupted last JSON line.
type Record struct {
	Manifest         Manifest    `json:"manifest"`
	Events           []TaskEvent `json:"events,omitempty"`
	Incomplete       bool        `json:"incomplete"`
	TruncatedEvents  bool        `json:"truncated_events,omitempty"`
	LegacyEventCount int         `json:"legacy_event_count,omitempty"`
}

func ValidOutcome(outcome string) bool {
	switch outcome {
	case "success", "task_failure", "prerequisite_failure", "canceled", "timed_out", "interrupted", "cleanup_incomplete":
		return true
	}
	return false
}

func ValidateTask(task Task) error {
	if task.Kind == "" {
		return fmt.Errorf("task kind is empty")
	}
	for _, digest := range []string{task.ImageDigest, task.PlanDigest, task.SourceDigest} {
		if digest != "" && !validDigest(digest) {
			return fmt.Errorf("invalid task digest")
		}
	}
	for _, input := range task.Inputs {
		if input.Name == "" {
			return fmt.Errorf("input name is empty")
		}
		if input.Secret && input.Digest != "" {
			return fmt.Errorf("secret input must not include a digest")
		}
		if input.Digest != "" && !validDigest(input.Digest) {
			return fmt.Errorf("invalid input digest")
		}
	}
	return nil
}

func ValidateArtifact(artifact Artifact) error {
	if artifact.Name == "" {
		return fmt.Errorf("artifact name is empty")
	}
	switch artifact.Status {
	case "present":
		if artifact.Path == "" || filepath.IsAbs(artifact.Path) || strings.Contains(artifact.Path, "\\") || filepath.ToSlash(filepath.Clean(artifact.Path)) != artifact.Path || artifact.Path == "." || strings.HasPrefix(artifact.Path, "../") {
			return fmt.Errorf("invalid artifact path: %q", artifact.Path)
		}
		if artifact.Size < 0 || artifact.Size > MaxArtifactBytes {
			return fmt.Errorf("artifact size exceeds bounds")
		}
		if !validDigest(artifact.Digest) {
			return fmt.Errorf("invalid artifact digest")
		}
	case "unavailable", "failed":
		if artifact.Reason == "" {
			return fmt.Errorf("unavailable artifact needs a reason")
		}
		if artifact.Path != "" || artifact.Digest != "" || artifact.Size != 0 {
			return fmt.Errorf("unavailable artifact must not claim file content")
		}
	default:
		return fmt.Errorf("invalid artifact status: %q", artifact.Status)
	}
	return nil
}

func validDigest(digest string) bool {
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil
}

// LoadRecord reads metadata and hashes bounded artifacts without loading their
// contents into memory. It never executes commands or follows artifact symlinks.
func LoadRecord(dir string) (Record, error) {
	var record Record
	data, err := readBoundedFile(filepath.Join(dir, "manifest.json"), MaxRecordBytes)
	if err != nil {
		if !os.IsNotExist(err) {
			return record, fmt.Errorf("read manifest: %w", err)
		}
	} else if err := json.Unmarshal(data, &record.Manifest); err != nil {
		return record, fmt.Errorf("parse manifest: %w", err)
	}
	manifest := &record.Manifest
	if manifest.SchemaVersion == SchemaVersion && manifest.EndedAt != "" && manifest.Outcome == "success" && manifest.PrimaryError != "" {
		return record, fmt.Errorf("successful manifest has a primary error")
	}
	if manifest.SchemaVersion != 0 && manifest.SchemaVersion != SchemaVersion {
		return record, fmt.Errorf("unsupported run schema: %d", manifest.SchemaVersion)
	}
	if manifest.RunID == "" {
		manifest.RunID = filepath.Base(dir)
	}
	record.Incomplete = manifest.EndedAt == ""
	if record.Incomplete {
		manifest.Outcome = "interrupted"
	} else if manifest.Outcome == "" {
		if manifest.ExitStatus == "ok" {
			manifest.Outcome = "success"
		} else {
			manifest.Outcome = "task_failure"
		}
	}
	if !ValidOutcome(manifest.Outcome) {
		return record, fmt.Errorf("invalid run outcome: %q", manifest.Outcome)
	}
	if manifest.Task != nil {
		if err := ValidateTask(*manifest.Task); err != nil {
			return record, err
		}
	}
	if len(manifest.Artifacts) > 1024 {
		return record, fmt.Errorf("too many artifacts")
	}
	var total int64
	names := make(map[string]bool)
	paths := make(map[string]bool)
	for _, artifact := range manifest.Artifacts {
		if names[artifact.Name] || (artifact.Path != "" && paths[artifact.Path]) {
			return record, fmt.Errorf("duplicate artifact record")
		}
		names[artifact.Name] = true
		if artifact.Path != "" {
			paths[artifact.Path] = true
		}
		if err := ValidateArtifact(artifact); err != nil {
			return record, err
		}
		if artifact.Status != "present" {
			continue
		}
		total += artifact.Size
		if total > 128<<20 {
			return record, fmt.Errorf("artifact verification exceeds bounds")
		}
		if err := verifyArtifact(dir, artifact); err != nil {
			return record, err
		}
	}
	var sequence uint64
	truncated, err := readJSONLines(filepath.Join(dir, "events.jsonl"), func(data []byte) error {
		if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
			return fmt.Errorf("event must be an object")
		}
		var event TaskEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		if event.SchemaVersion == 0 {
			record.LegacyEventCount++
			return nil
		}
		if event.SchemaVersion != SchemaVersion {
			return fmt.Errorf("unsupported event schema: %d", event.SchemaVersion)
		}
		if event.RunID != manifest.RunID || event.AttemptID == "" || (manifest.AttemptID != "" && event.AttemptID != manifest.AttemptID) || event.Sequence != sequence+1 || event.Kind == "" {
			return fmt.Errorf("invalid task event identity or sequence")
		}
		sequence = event.Sequence
		record.Events = append(record.Events, event)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return record, err
	}
	record.TruncatedEvents = truncated
	return record, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := openBoundedRecord(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds bounds")
	}
	return data, nil
}

func verifyArtifact(dir string, artifact Artifact) error {
	path := dir
	for _, component := range strings.Split(artifact.Path, "/") {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("stat artifact %q: %w", artifact.Name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact symlink refused: %q", artifact.Path)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(filepath.FromSlash(artifact.Path))
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return fmt.Errorf("artifact size/type mismatch: %q", artifact.Name)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, artifact.Size+1))
	if err != nil {
		return err
	}
	if n != artifact.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != artifact.Digest {
		return fmt.Errorf("artifact digest mismatch: %q", artifact.Name)
	}
	return nil
}

func readJSONLines(path string, consume func([]byte) error) (bool, error) {
	file, err := openBoundedRecord(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxEventBytes+1))
	scanner.Buffer(make([]byte, 4096), MaxRecordBytes)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i+1], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	total := 0
	for line := 1; scanner.Scan(); line++ {
		data := scanner.Bytes()
		total += len(data)
		if total > maxEventBytes {
			return false, fmt.Errorf("event file exceeds bounds")
		}
		if !json.Valid(data) {
			var value any
			err := json.Unmarshal(data, &value)
			if !bytes.HasSuffix(data, []byte{'\n'}) && strings.Contains(err.Error(), "unexpected end of JSON input") {
				return true, nil
			}
			return false, fmt.Errorf("read %s line %d: %w", filepath.Base(path), line, err)
		}
		if err := consume(data); err != nil {
			return false, fmt.Errorf("read %s line %d: %w", filepath.Base(path), line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read events: %w", err)
	}
	return false, nil
}

func openBoundedRecord(path string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("record is not a regular file")
	}
	return root.Open(name)
}
