package coved

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/fleetcontrol"
)

func TestFleetWorkerRegisterHeartbeatAndAwait(t *testing.T) {
	vmRoot := t.TempDir()
	mustMkdirAll(t, filepath.Join(vmRoot, "vm-a"))
	imageRoot := t.TempDir()
	writeManifest(t, imageRoot, "base", "v1")
	writeManifest(t, imageRoot, "nested", "image", "latest")

	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
		Host:          "mini.local",
		Version:       "test-version",
		VMRoot:        vmRoot,
		ImageRoot:     imageRoot,
		Labels:        map[string]string{"zone": "desk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := worker.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	assignment, err := worker.AwaitAssignment(ctx)
	if err != nil {
		t.Fatalf("AwaitAssignment: %v", err)
	}
	if assignment != nil {
		t.Fatalf("assignment = %+v, want nil", assignment)
	}
	record, ok := store.Get("worker-1")
	if !ok {
		t.Fatal("worker not registered")
	}
	if record.ID != "worker-1" || record.Host != "mini.local" || record.Version != "test-version" {
		t.Fatalf("worker identity = %+v", record)
	}
	if record.Labels["zone"] != "desk" {
		t.Fatalf("worker labels = %#v", record.Labels)
	}
	if record.Capacity.CPUs <= 0 || record.Capacity.VMs != 1 || record.Capacity.Images != 2 {
		t.Fatalf("worker capacity = %+v", record.Capacity)
	}
}

func TestFleetWorkerReportsUnsupportedAssignment(t *testing.T) {
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := store.CreateAssignment(fleetcontrol.Assignment{ID: "assignment-1", WorkerID: "worker-1", Verb: "run"}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment: %v", err)
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	report := assignment.LastReport
	if report == nil || report.ID != "worker-1" || report.AssignmentID != "assignment-1" || report.Status != "unsupported" {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Error, `unsupported assignment verb "run"`) {
		t.Fatalf("report error = %q", report.Error)
	}
}

func TestFleetWorkerCompletesNoopAssignment(t *testing.T) {
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := store.CreateAssignment(fleetcontrol.Assignment{ID: "assignment-1", WorkerID: "worker-1", Verb: "noop"}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment: %v", err)
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	if assignment.Status != "complete" || assignment.LastReport == nil || assignment.LastReport.Status != "complete" {
		t.Fatalf("assignment = %+v", assignment)
	}
}

func TestFleetWorkerRunsCoveAssignment(t *testing.T) {
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
printf 'stdout:%s\n' "$*"
printf 'stderr:%s\n' "$*" >&2
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
		CoveBin:       coveBin,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := store.CreateAssignment(fleetcontrol.Assignment{
		ID:       "assignment-1",
		WorkerID: "worker-1",
		Verb:     "cove",
		Args:     []string{"run", "-ephemeral"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment: %v", err)
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	report := assignment.LastReport
	if assignment.Status != "complete" || report == nil || report.ExitCode != 0 {
		t.Fatalf("assignment = %+v", assignment)
	}
	if !strings.Contains(report.Stdout, "stdout:run -ephemeral") || !strings.Contains(report.Stderr, "stderr:run -ephemeral") {
		t.Fatalf("report output = stdout %q stderr %q", report.Stdout, report.Stderr)
	}
}

func TestFleetWorkerReportsCoveAssignmentFailure(t *testing.T) {
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
printf 'this output is deliberately long\n'
printf 'boom\n' >&2
exit 7
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
		CoveBin:       coveBin,
		OutputLimit:   12,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := store.CreateAssignment(fleetcontrol.Assignment{ID: "assignment-1", WorkerID: "worker-1", Verb: "cove"}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment: %v", err)
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	report := assignment.LastReport
	if assignment.Status != "failed" || report == nil || report.ExitCode != 7 {
		t.Fatalf("assignment = %+v", assignment)
	}
	if !strings.Contains(report.Error, "exit status 7") {
		t.Fatalf("report error = %q", report.Error)
	}
	if report.Stdout != "this output " {
		t.Fatalf("stdout = %q, want truncated output", report.Stdout)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, root string, parts ...string) {
	t.Helper()
	dir := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-cove")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
