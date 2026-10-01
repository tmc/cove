// run_bundle.go — per-run artifact bundling for `cove run -fork-from`.
//
// Each ephemeral fork-from run gets a short hex run ID and a directory
// under ~/.vz/runs/<run-id>/ that holds:
//
//	manifest.json    {run_id, vm_name, fork_from, started_at, ended_at, exit_status}
//	events.jsonl     append-only control-socket event log (one JSON per line)
//	stdout.log       tee of process stdout
//	stderr.log       tee of process stderr
//	screenshots/     captures recorded during the run
//
// The directory is created lazily on first write, so `cove run` invocations
// that exit before booting do not litter empty bundle dirs. The manifest is
// created on first write and replaced atomically (temp + rename) on shutdown — both success and failure
// paths.
//
// Plain `cove run <vm>` (no -fork-from) does NOT create a bundle: long-lived
// workstations are not jobs to bisect. Bundling is opt-in via the runtime.

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	runmetrics "github.com/tmc/cove/internal/metrics"
	"github.com/tmc/cove/internal/runs"
	"github.com/tmc/cove/internal/vmconfig"
)

// RunBundle owns the on-disk per-run directory. The zero value is not usable;
// construct via NewRunBundle. The dir field stores the target path but is
// only created on first write (see ensureDir).
type RunBundle struct {
	id               string
	dir              string
	vmName           string
	forkFrom         string
	startedAt        time.Time
	endedAt          time.Time
	exitStatus       string
	finalized        bool
	created          bool
	mu               sync.Mutex
	eventsFile       *os.File
	stdoutFile       *os.File
	stderrFile       *os.File
	metricsSink      runmetrics.Sink
	metricAgentReady bool
	vmDir            string
	resourceSampled  map[string]bool
	resourceSampler  *resourceSampler
	resourceSampleN  int64
	task             *runs.Task
	attemptID        string
	sequence         uint64
	outcome          string
	primaryError     string
	cleanupErrors    []string
	captureErrors    []string
	artifacts        []runs.Artifact
	screenshotN      int
}

// runManifest is the on-disk schema for manifest.json.
type runManifest = runs.Manifest

// NewRunBundle prepares a bundle for the given run. The bundle directory
// itself is not created until the first write — call ensureDir explicitly
// if you need the path to exist eagerly.
func NewRunBundle(runsRoot, vmName, forkFrom string) (*RunBundle, error) {
	id, err := generateRunID()
	if err != nil {
		return nil, fmt.Errorf("generate run id: %w", err)
	}
	return &RunBundle{
		id:        id,
		attemptID: id,
		dir:       filepath.Join(runsRoot, id),
		vmName:    vmName,
		forkFrom:  forkFrom,
		startedAt: time.Now().UTC(),
	}, nil
}

// ID returns the short hex run id.
func (b *RunBundle) ID() string {
	if b == nil {
		return ""
	}
	return b.id
}

// Dir returns the bundle directory path. The directory may not yet exist
// on disk if no events have been recorded.
func (b *RunBundle) Dir() string {
	if b == nil {
		return ""
	}
	return b.dir
}

func (b *RunBundle) SetVMDir(dir string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.vmDir = dir
	b.mu.Unlock()
}

// ScreenshotsDir returns the screenshots/ subdirectory under the bundle.
// The directory is not created until RecordScreenshot is called.
func (b *RunBundle) ScreenshotsDir() string {
	if b == nil {
		return ""
	}
	return filepath.Join(b.dir, "screenshots")
}

// AppendEvent writes a single JSON event line to events.jsonl, creating the
// bundle directory and event file lazily on first call. It is safe to call
// concurrently. A nil bundle no-ops.
func (b *RunBundle) AppendEvent(event map[string]any) error {
	if b == nil || event == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized || !b.endedAt.IsZero() {
		return fmt.Errorf("run bundle is finalized")
	}
	copyEvent := make(map[string]any, len(event)+6)
	for key, value := range event {
		copyEvent[key] = value
	}
	if _, ok := copyEvent["ts"]; !ok {
		copyEvent["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	copyEvent["schema_version"] = runs.SchemaVersion
	copyEvent["run_id"] = b.id
	copyEvent["attempt_id"] = b.attemptID
	copyEvent["sequence"] = b.sequence + 1
	if kind, _ := copyEvent["kind"].(string); kind == "" {
		kind, _ = copyEvent["event"].(string)
		if kind == "" {
			kind = "observation"
		}
		copyEvent["kind"] = kind
	}
	data, err := json.Marshal(copyEvent)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if len(data) >= runs.MaxRecordBytes {
		return fmt.Errorf("event exceeds bounds")
	}
	if err := b.ensureDirLocked(); err != nil {
		return err
	}
	if b.eventsFile == nil {
		f, err := os.OpenFile(filepath.Join(b.dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open events.jsonl: %w", err)
		}
		b.eventsFile = f
	}
	if _, err := b.eventsFile.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	b.sequence++
	return nil
}

// ConfigureTask fixes declared task provenance before the first event.
func (b *RunBundle) ConfigureTask(task runs.Task) error {
	if b == nil {
		return nil
	}
	if err := runs.ValidateTask(task); err != nil {
		return err
	}
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	if len(data) > runs.MaxRecordBytes/2 {
		return fmt.Errorf("task provenance exceeds bounds")
	}
	var copied runs.Task
	if err := json.Unmarshal(data, &copied); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.created || b.finalized || b.task != nil {
		return fmt.Errorf("task provenance is already fixed")
	}
	b.task = &copied
	return nil
}

// AppendTaskEvent records an event with writer-assigned identity and order.
func (b *RunBundle) AppendTaskEvent(event runs.TaskEvent) error {
	if b == nil {
		return nil
	}
	if event.Kind == "" {
		return fmt.Errorf("task event kind is empty")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if event.Timestamp == "" {
		delete(values, "ts")
	}
	return b.AppendEvent(values)
}

// RecordArtifact records a bounded diagnostic or its explicit unavailability.
// File paths must be under artifacts/; data must already be redacted.
func (b *RunBundle) RecordArtifact(artifact runs.Artifact, data []byte) error {
	if b == nil {
		return nil
	}
	if artifact.Status == "present" {
		if !strings.HasPrefix(artifact.Path, "artifacts/") {
			return fmt.Errorf("artifact path must be under artifacts/")
		}
		if int64(len(data)) > runs.MaxArtifactBytes {
			return fmt.Errorf("artifact size exceeds bounds")
		}
		artifact.Size = int64(len(data))
		digest := sha256.Sum256(data)
		artifact.Digest = "sha256:" + hex.EncodeToString(digest[:])
	} else if len(data) != 0 {
		return fmt.Errorf("unavailable artifact must not include data")
	}
	if err := runs.ValidateArtifact(artifact); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized || !b.endedAt.IsZero() {
		return fmt.Errorf("run bundle is finalized")
	}
	for _, existing := range b.artifacts {
		if existing.Name == artifact.Name || (artifact.Path != "" && existing.Path == artifact.Path) {
			return fmt.Errorf("artifact already recorded: %s", artifact.Name)
		}
	}
	var artifactBytes int64
	for _, existing := range b.artifacts {
		artifactBytes += existing.Size
	}
	if artifactBytes+artifact.Size > 128<<20 {
		return fmt.Errorf("artifact collection exceeds bounds")
	}
	if len(b.artifacts) >= 1024 {
		return fmt.Errorf("too many artifacts")
	}
	if err := b.ensureDirLocked(); err != nil {
		return err
	}
	if artifact.Status == "present" {
		root, err := os.OpenRoot(b.dir)
		if err != nil {
			return err
		}
		defer root.Close()
		path := filepath.FromSlash(artifact.Path)
		if err := root.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		componentPath := ""
		for _, component := range strings.Split(artifact.Path, "/") {
			componentPath = filepath.Join(componentPath, component)
			info, err := root.Lstat(componentPath)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("artifact symlink refused")
			}
		}
		id, err := generateRunID()
		if err != nil {
			return err
		}
		temporary := filepath.Join(filepath.Dir(path), ".artifact-"+id)
		defer root.Remove(temporary)
		file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := root.Rename(temporary, path); err != nil {
			return err
		}
	}
	b.artifacts = append(b.artifacts, artifact)
	if artifact.Status == "failed" {
		b.captureErrors = append(b.captureErrors, artifact.Name+": "+artifact.Reason)
	}
	return b.writeManifestLocked()
}

// RecordScreenshot copies pre-encoded image bytes into screenshots/<name>.
// If name has no extension, .png is appended. The directory is created
// lazily. A nil bundle no-ops.
func (b *RunBundle) RecordScreenshot(name string, data []byte) error {
	if b == nil {
		return nil
	}
	if name == "" {
		name = fmt.Sprintf("screenshot_%d.png", time.Now().UnixNano())
	}
	if filepath.Ext(name) == "" {
		name += ".png"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized || !b.endedAt.IsZero() {
		return fmt.Errorf("run bundle is finalized")
	}
	if err := b.ensureDirLocked(); err != nil {
		return err
	}
	dir := filepath.Join(b.dir, "screenshots")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create screenshots dir: %w", err)
	}
	path := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write screenshot: %w", err)
	}
	b.screenshotN++
	return nil
}

// StdoutWriter returns a writer that tees process stdout into stdout.log.
// The file is opened lazily on first write. Returns nil for a nil bundle.
func (b *RunBundle) StdoutWriter() *bundleWriter {
	if b == nil {
		return nil
	}
	return &bundleWriter{bundle: b, name: "stdout.log", fp: &b.stdoutFile}
}

// StderrWriter mirrors StdoutWriter for stderr.log.
func (b *RunBundle) StderrWriter() *bundleWriter {
	if b == nil {
		return nil
	}
	return &bundleWriter{bundle: b, name: "stderr.log", fp: &b.stderrFile}
}

// Finalize writes manifest.json atomically (temp + rename) and closes any
// open file handles. exitStatus is recorded as "ok" for nil err, otherwise
// the err.Error() text. Safe to call once; subsequent calls no-op.
func (b *RunBundle) Finalize(exitErr error) error {
	outcome := "success"
	if errors.Is(exitErr, context.Canceled) {
		outcome = "canceled"
	} else if errors.Is(exitErr, context.DeadlineExceeded) {
		outcome = "timed_out"
	} else if exitErr != nil {
		outcome = "task_failure"
	}
	return b.FinalizeTask(outcome, exitErr, nil)
}

// FinalizeTask preserves the primary error separately from capture and cleanup.
// A failed manifest publication can be retried without changing the first result.
func (b *RunBundle) FinalizeTask(outcome string, exitErr error, cleanupErrors []string) error {
	if b == nil {
		return nil
	}
	if !runs.ValidOutcome(outcome) {
		return fmt.Errorf("invalid task outcome: %q", outcome)
	}
	if outcome == "success" && exitErr != nil {
		return fmt.Errorf("successful task must not have a primary error")
	}
	b.stopResourceSampler()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized {
		return nil
	}
	if b.endedAt.IsZero() {
		b.outcome = outcome
		b.cleanupErrors = append([]string(nil), cleanupErrors...)
		if outcome == "success" && len(cleanupErrors) != 0 {
			b.outcome = "cleanup_incomplete"
		}
		b.endedAt = time.Now().UTC()
		if exitErr != nil {
			b.exitStatus = exitErr.Error()
			b.primaryError = exitErr.Error()
		} else {
			b.exitStatus = "ok"
		}

	}

	if b.eventsFile != nil {
		_ = b.eventsFile.Close()
		b.eventsFile = nil
	}
	if b.stdoutFile != nil {
		_ = b.stdoutFile.Close()
		b.stdoutFile = nil
	}
	if b.stderrFile != nil {
		_ = b.stderrFile.Close()
		b.stderrFile = nil
	}
	if b.metricsSink != nil {
		_ = b.metricsSink.Close()
		b.metricsSink = nil
	}

	if !b.created {
		b.finalized = true
		// No events ever recorded: keep the disk clean and skip the
		// manifest. The run effectively never produced telemetry.
		return nil
	}
	if err := b.writeManifestLocked(); err != nil {
		return err
	}
	b.finalized = true
	return nil
}

// EmitMetric writes one structured metric event into metrics.jsonl.
func (b *RunBundle) EmitMetric(ctx context.Context, event runmetrics.Event) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized || !b.endedAt.IsZero() {
		return nil
	}
	if err := b.ensureDirLocked(); err != nil {
		return err
	}
	if b.metricsSink == nil {
		sink, err := newMetricsSink(filepath.Join(b.dir, "metrics.jsonl"))
		if err != nil {
			return err
		}
		b.metricsSink = sink
	}
	return b.metricsSink.Emit(ctx, event)
}

func (b *RunBundle) writeManifestLocked() error {
	mf := runManifest{
		SchemaVersion: runs.SchemaVersion,
		AttemptID:     b.attemptID,
		Outcome:       b.outcome,
		PrimaryError:  b.primaryError,
		CleanupErrors: b.cleanupErrors,
		CaptureErrors: b.captureErrors,
		Task:          b.task,
		Artifacts:     b.artifacts,
		RunID:         b.id,
		VMName:        b.vmName,
		ForkFrom:      b.forkFrom,
		StartedAt:     b.startedAt.Format(time.RFC3339Nano),

		ExitStatus: b.exitStatus,
	}
	if !b.endedAt.IsZero() {
		mf.EndedAt = b.endedAt.Format(time.RFC3339Nano)
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if len(data) >= runs.MaxRecordBytes {
		return fmt.Errorf("run manifest exceeds bounds")
	}
	finalPath := filepath.Join(b.dir, "manifest.json")
	tmpPath, err := atomicWriteFile(finalPath, append(data, '\n'), 0644)
	if err != nil {
		// best-effort cleanup of any temp left behind
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
		return err
	}
	return nil
}

// ensureDirLocked creates the bundle dir on first use. Caller holds b.mu.
func (b *RunBundle) ensureDirLocked() error {
	if b.created {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(b.dir), 0755); err != nil {
		return fmt.Errorf("create runs root: %w", err)
	}
	if err := os.Mkdir(b.dir, 0700); err != nil {
		return fmt.Errorf("create bundle dir: %w", err)
	}
	b.created = true
	if err := b.writeManifestLocked(); err != nil {
		b.created = false
		os.Remove(b.dir)
		return err
	}

	return nil
}

// bundleWriter implements io.Writer by appending to a file inside the bundle.
// The file is opened lazily on first write so a writer handed to MultiWriter
// does not force the bundle dir into existence at runtime startup.
type bundleWriter struct {
	bundle *RunBundle
	name   string
	fp     **os.File
}

// Write implements io.Writer. Writes outside the bundle path are silently
// discarded if the bundle is closed; this keeps the writer safe to hand to
// long-lived stdio pipes that may outlive Finalize.
func (w *bundleWriter) Write(p []byte) (int, error) {
	if w == nil || w.bundle == nil {
		return len(p), nil
	}
	w.bundle.mu.Lock()
	defer w.bundle.mu.Unlock()
	if w.bundle.finalized || !w.bundle.endedAt.IsZero() {
		return len(p), nil
	}
	if err := w.bundle.ensureDirLocked(); err != nil {
		return 0, err
	}
	if *w.fp == nil {
		f, err := os.OpenFile(filepath.Join(w.bundle.dir, w.name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
		*w.fp = f
	}
	return (*w.fp).Write(p)
}

// runsDirHook returns the on-disk root for run bundles. Indirected through
// a var so tests can swap in t.TempDir() without touching $HOME.
var runsDirHook = vmconfig.RunsDir

// beginRunBundle creates the bundle for a fork-from run and registers it
// as the active bundle. Emits a "run.start" event so the directory is
// created lazily on the first real event (per AC #3) without a separate
// eager-mkdir step. Returns nil on failure but always logs — bundling is
// best-effort and never fails the run.
func beginRunBundle(cfg RunConfig) (*RunBundle, error) {
	vmName := cfg.EphemeralForkName
	if vmName == "" {
		vmName = cfg.VM.Name
	}
	b, err := NewRunBundle(runsDirHook(), vmName, cfg.EphemeralForkParent)
	if err != nil {
		return nil, err
	}
	if verbose {
		fmt.Printf("run bundle: %s (%s)\n", b.ID(), b.Dir())
	}
	if err := b.AppendEvent(map[string]any{
		"event":     "run.start",
		"run_id":    b.ID(),
		"vm_name":   vmName,
		"fork_from": cfg.EphemeralForkParent,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: run bundle start event: %v\n", err)
	}
	return b, nil
}

// finishRunBundle records the exit event and finalizes the manifest.
// Safe to call with a nil bundle.
func finishRunBundle(b *RunBundle, runErr error) {
	if b == nil {
		return
	}
	exit := "ok"
	if runErr != nil {
		exit = runErr.Error()
	}
	if err := b.AppendEvent(map[string]any{
		"event":       "run.exit",
		"run_id":      b.ID(),
		"exit_status": exit,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: run bundle exit event: %v\n", err)
	}
	b.EmitMetricEvent("run_complete", b.startedAt, exit, nil)
	if err := b.Finalize(runErr); err != nil {
		fmt.Fprintf(os.Stderr, "warning: run bundle finalize: %v\n", err)
	}
}

// TeeControlEvent appends a one-line event to the bundle for each
// control-socket request handled.
//
// resp is intentionally typed by interface so the unit tests in this package
// can synthesize fake responses without pulling in controlpb.
func (b *RunBundle) TeeControlEvent(reqType string, resp interface{ GetError() string }) {
	if b == nil {
		return
	}
	event := map[string]any{
		"event":    "control",
		"req_type": reqType,
	}
	if resp != nil {
		if errStr := resp.GetError(); errStr != "" {
			event["error"] = errStr
		} else if reqType == "agent-ping" {
			b.MarkAgentReady()
		}
	}
	_ = b.AppendEvent(event)
}

// generateRunID returns a short 8-char hex string suitable for use as a
// run id. Uses crypto/rand so concurrent invocations stay collision-safe.
func generateRunID() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// atomicWriteFile writes data to a sibling temp file then renames it over
// finalPath. Returns the temp path used (so callers can clean up on error)
// alongside any failure.
func atomicWriteFile(finalPath string, data []byte, mode os.FileMode) (string, error) {
	dir := filepath.Dir(finalPath)
	tmp, err := os.CreateTemp(dir, filepath.Base(finalPath)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return tmpPath, fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return tmpPath, fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return tmpPath, fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return tmpPath, fmt.Errorf("rename temp: %w", err)
	}
	return "", nil
}
