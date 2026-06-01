package coved

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/fleetcontrol"
)

const fleetWorkerTestTimeout = 5 * time.Second

func TestFleetWorkerRegisterHeartbeatAndAwait(t *testing.T) {
	vmRoot := t.TempDir()
	mustMkdirAll(t, filepath.Join(vmRoot, "vm-a"))
	imageRoot := t.TempDir()
	writeManifestWithDigest(t, imageRoot, "sha256:base", "base", "v1")
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
		Capabilities:  []string{"ram-overlay", "asif", "ram-overlay"},
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
	if strings.Join(record.Capabilities, ",") != "asif,ram-overlay" {
		t.Fatalf("worker capabilities = %+v, want asif/ram-overlay", record.Capabilities)
	}
	if record.Capacity.CPUs <= 0 || record.Capacity.VMs != 1 || record.Capacity.Images != 2 {
		t.Fatalf("worker capacity = %+v", record.Capacity)
	}
	if record.Capacity.MaxVMs != record.Capacity.CPUs {
		t.Fatalf("worker max VMs = %d, want CPU count %d", record.Capacity.MaxVMs, record.Capacity.CPUs)
	}
	wantRefs := []string{"base:v1", "nested/image:latest"}
	if strings.Join(record.ImageRefs, ",") != strings.Join(wantRefs, ",") {
		t.Fatalf("image refs = %+v, want %+v", record.ImageRefs, wantRefs)
	}
	if len(record.ImageDetails) != 2 || record.ImageDetails[0].Ref != "base:v1" || record.ImageDetails[0].SourceManifestDigest != "sha256:base" {
		t.Fatalf("image details = %+v, want base digest plus nested image", record.ImageDetails)
	}
}

func TestFleetWorkerMergesDiscoveredCapabilities(t *testing.T) {
	previous := discoverFleetCapabilities
	discoverFleetCapabilities = func() []string {
		return []string{FleetCapabilityRAMOverlay, FleetCapabilityRAMOverlay}
	}
	t.Cleanup(func() {
		discoverFleetCapabilities = previous
	})

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: "http://127.0.0.1:9758",
		ID:            "worker-1",
		Capabilities:  []string{"asif", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(worker.heartbeat().Capabilities, ","); got != "asif,ram-overlay" {
		t.Fatalf("heartbeat capabilities = %q, want asif,ram-overlay", got)
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

func TestFleetWorkerRunsControlAssignment(t *testing.T) {
	vmRoot := shortTempDir(t)
	vmName := "cove-sandbox-job-1"
	vmDir := filepath.Join(vmRoot, vmName)
	mustMkdirAll(t, vmDir)
	if err := os.WriteFile(filepath.Join(vmDir, "control.token"), []byte("tok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(vmDir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requests := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var request map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &request); err != nil {
			return
		}
		requests <- request
		_, _ = conn.Write([]byte(`{"success":true,"screenshot_result":{"image_data":"cG5n"}}` + "\n"))
	}()

	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
		VMRoot:        vmRoot,
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
		Verb:     "cove-control",
		Args:     []string{vmName, `{"type":"screenshot","screenshot":{"format":"png"}}`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment: %v", err)
	}
	select {
	case request := <-requests:
		if request["auth_token"] != "tok" || request["type"] != "screenshot" {
			t.Fatalf("control request = %+v, want token and screenshot", request)
		}
	case <-time.After(fleetWorkerTestTimeout):
		t.Fatal("control request not received")
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	report := assignment.LastReport
	if assignment.Status != "complete" || report == nil || report.ExitCode != 0 || !strings.Contains(report.Stdout, `"image_data":"cG5n"`) {
		t.Fatalf("assignment = %+v", assignment)
	}
}

func TestFleetWorkerRunsWarmPoolAssignment(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("COVE_TEST_ARGS", argsPath)
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
if [ "$1" = "shell" ]; then
  exit 0
fi
printf '%s\n' "$*" > "$COVE_TEST_ARGS"
sleep 0.2
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL:      server.URL,
		ID:                 "worker-1",
		CoveBin:            coveBin,
		AssignmentInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := store.EnsureWarmPool(fleetcontrol.WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 1 {
		t.Fatalf("created = %+v, want 1", result.Created)
	}
	assignmentID := result.Created[0].ID
	done := make(chan error, 1)
	go func() {
		done <- worker.PollAssignment(ctx)
	}()
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment(assignmentID)
		return ok && assignment.Status == "ready" && assignment.LastReport != nil && assignment.LastReport.Status == "ready"
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PollAssignment: %v", err)
		}
	case <-time.After(fleetWorkerTestTimeout):
		t.Fatal("PollAssignment did not finish")
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	if !strings.Contains(got, "run -fork-from base:v1") || !strings.Contains(got, "-ephemeral -keep -headless") {
		t.Fatalf("warm pool cove args = %q", got)
	}
	assignment, ok := store.GetAssignment(assignmentID)
	if !ok {
		t.Fatal("assignment missing")
	}
	if assignment.Status != "complete" || assignment.WarmPool != "runner" {
		t.Fatalf("assignment = %+v", assignment)
	}
}

func TestFleetWorkerMarksSandboxReady(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("COVE_TEST_ARGS", argsPath)
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
if [ "$1" = "shell" ]; then
  exit 0
fi
printf '%s\n' "$*" > "$COVE_TEST_ARGS"
sleep 0.2
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL:      server.URL,
		ID:                 "worker-1",
		CoveBin:            coveBin,
		AssignmentInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	sandbox, err := store.CreateSandbox(fleetcontrol.SandboxRequest{ID: "job-1", ImageRef: "base:v1"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- worker.PollAssignment(ctx)
	}()
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment(sandbox.Assignment.ID)
		return ok && assignment.Status == "ready" && assignment.LastReport != nil && assignment.LastReport.Status == "ready"
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PollAssignment: %v", err)
		}
	case <-time.After(fleetWorkerTestTimeout):
		t.Fatal("PollAssignment did not finish")
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	if !strings.Contains(got, "run -fork-from base:v1") || !strings.Contains(got, "-fork-name cove-sandbox-job-1") {
		t.Fatalf("sandbox cove args = %q", got)
	}
	assignment, ok := store.GetAssignment(sandbox.Assignment.ID)
	if !ok {
		t.Fatal("assignment missing")
	}
	if assignment.Status != "complete" || assignment.SandboxID != "job-1" || assignment.SandboxRole != "run" {
		t.Fatalf("assignment = %+v", assignment)
	}
}

func TestFleetWorkerDoesNotClaimWarmPoolBeforeReady(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("COVE_TEST_RELEASE", release)
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
if [ "$1" = "shell" ]; then
  exit 1
fi
while [ ! -f "$COVE_TEST_RELEASE" ]; do
  sleep 0.02
done
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL:      server.URL,
		ID:                 "worker-1",
		CoveBin:            coveBin,
		AssignmentInterval: 20 * time.Millisecond,
		AssignmentTimeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := store.EnsureWarmPool(fleetcontrol.WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	assignmentID := result.Created[0].ID
	done := make(chan error, 1)
	go func() {
		done <- worker.PollAssignment(ctx)
	}()
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment(assignmentID)
		return ok && assignment.Status == "running" && assignment.LastReport != nil && assignment.LastReport.Status == "running"
	})
	_, err = store.ClaimWarmPool(fleetcontrol.WarmPoolClaimRequest{Name: "runner", Command: []string{"/bin/true"}})
	if err == nil || !strings.Contains(err.Error(), "has no ready slot to claim") {
		t.Fatalf("ClaimWarmPool err = %v, want no ready slot error", err)
	}
	if err := os.WriteFile(release, []byte("done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PollAssignment: %v", err)
		}
	case <-time.After(fleetWorkerTestTimeout):
		t.Fatal("PollAssignment did not finish")
	}
}

func TestFleetWorkerExecutesClaimedWarmPoolAssignment(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	shellArgs := filepath.Join(t.TempDir(), "shell-args.txt")
	stopArgs := filepath.Join(t.TempDir(), "stop-args.txt")
	t.Setenv("COVE_TEST_RELEASE", release)
	t.Setenv("COVE_TEST_SHELL_ARGS", shellArgs)
	t.Setenv("COVE_TEST_STOP_ARGS", stopArgs)
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
if [ "$1" = "shell" ]; then
  if [ "$4" = "/bin/sh" ] && [ "$5" = "-c" ] && [ "$6" = "true" ]; then
    exit 0
  fi
  printf '%s\n' "$*" > "$COVE_TEST_SHELL_ARGS"
  exit 0
fi
if [ "$1" = "ctl" ]; then
  printf '%s\n' "$*" > "$COVE_TEST_STOP_ARGS"
  touch "$COVE_TEST_RELEASE"
  exit 0
fi
while [ ! -f "$COVE_TEST_RELEASE" ]; do
  sleep 0.02
done
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL:      server.URL,
		ID:                 "worker-1",
		CoveBin:            coveBin,
		AssignmentInterval: 20 * time.Millisecond,
		AssignmentTimeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := store.EnsureWarmPool(fleetcontrol.WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	slotID := result.Created[0].ID
	warmDone := make(chan error, 1)
	go func() {
		warmDone <- worker.PollAssignment(ctx)
	}()
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment(slotID)
		return ok && assignment.Status == "ready"
	})
	claim, err := store.ClaimWarmPool(fleetcontrol.WarmPoolClaimRequest{Name: "runner", Command: []string{"/bin/echo", "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment claim: %v", err)
	}
	assignment, ok := store.GetAssignment(claim.Assignment.ID)
	if !ok || assignment.Status != "complete" {
		t.Fatalf("claim assignment = %+v, ok=%v", assignment, ok)
	}
	data, err := os.ReadFile(shellArgs)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	want := "shell " + claim.VMName + " -- /bin/echo ok"
	if got != want {
		t.Fatalf("claim shell args = %q, want %q", got, want)
	}
	data, err = os.ReadFile(stopArgs)
	if err != nil {
		t.Fatal(err)
	}
	got = strings.TrimSpace(string(data))
	want = "ctl -vm " + claim.VMName + " stop"
	if got != want {
		t.Fatalf("claim cleanup args = %q, want %q", got, want)
	}
	select {
	case err := <-warmDone:
		if err != nil {
			t.Fatalf("warm PollAssignment: %v", err)
		}
	case <-time.After(fleetWorkerTestTimeout):
		t.Fatal("warm PollAssignment did not finish")
	}
}

func TestFleetWorkerDoesNotCleanupWarmPoolStopAssignment(t *testing.T) {
	stopArgs := filepath.Join(t.TempDir(), "stop-args.txt")
	t.Setenv("COVE_TEST_STOP_ARGS", stopArgs)
	coveBin := writeExecutable(t, `#!/bin/sh
printf '%s\n' "$*" > "$COVE_TEST_STOP_ARGS"
exit 0
`)
	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: "http://127.0.0.1:1",
		ID:            "worker-1",
		CoveBin:       coveBin,
	})
	if err != nil {
		t.Fatal(err)
	}
	report := worker.finishCoveAssignment(context.Background(), fleetcontrol.Assignment{
		ID:           "cleanup",
		WarmPoolSlot: "slot-1",
		Verb:         "cove",
		Args:         []string{"ctl", "-vm", "cove-warm-runner-slot-1", "stop"},
	}, fleetcontrol.WorkerReport{AssignmentID: "cleanup", Status: "complete"})
	if report.Status != "complete" || report.Error != "" {
		t.Fatalf("report = %+v, want unchanged complete report", report)
	}
	if _, err := os.Stat(stopArgs); !os.IsNotExist(err) {
		t.Fatalf("stop command ran during finish: stat err = %v", err)
	}
}

func TestFleetWorkerRefreshesImageRefsAfterPrepare(t *testing.T) {
	imageRoot := t.TempDir()
	t.Setenv("COVE_TEST_IMAGE_ROOT", imageRoot)
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
mkdir -p "$COVE_TEST_IMAGE_ROOT/base/v1"
printf '{}\n' > "$COVE_TEST_IMAGE_ROOT/base/v1/manifest.json"
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
		CoveBin:       coveBin,
		ImageRoot:     imageRoot,
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
		ImageRef: "base:v1",
		Verb:     "cove",
		Args:     []string{"image", "pull", "-tag", "base:v1", "registry.example/base:v1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment: %v", err)
	}
	record, ok := store.Get("worker-1")
	if !ok {
		t.Fatal("worker missing")
	}
	if strings.Join(record.ImageRefs, ",") != "base:v1" {
		t.Fatalf("image refs = %+v, want base:v1", record.ImageRefs)
	}
}

func TestFleetWorkerRefreshesImageRefsAfterImageGC(t *testing.T) {
	imageRoot := t.TempDir()
	writeManifest(t, imageRoot, "base", "v1")
	t.Setenv("COVE_TEST_IMAGE_ROOT", imageRoot)
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
rm -f "$COVE_TEST_IMAGE_ROOT/base/v1/manifest.json"
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: server.URL,
		ID:            "worker-1",
		CoveBin:       coveBin,
		ImageRoot:     imageRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := store.PushImageGC(fleetcontrol.ImageGCRequest{Apply: true}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatalf("PollAssignment: %v", err)
	}
	record, ok := store.Get("worker-1")
	if !ok {
		t.Fatal("worker missing")
	}
	if len(record.ImageRefs) != 0 {
		t.Fatalf("image refs = %+v, want none after gc", record.ImageRefs)
	}
}

func TestFleetWorkerRenewsRunningCoveAssignment(t *testing.T) {
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
sleep 0.2
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL:      server.URL,
		ID:                 "worker-1",
		CoveBin:            coveBin,
		AssignmentInterval: 20 * time.Millisecond,
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
	done := make(chan error, 1)
	go func() {
		done <- worker.PollAssignment(ctx)
	}()
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment("assignment-1")
		return ok && assignment.Status == "running" && assignment.LastReport != nil && assignment.LastReport.Status == "running"
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PollAssignment: %v", err)
		}
	case <-time.After(fleetWorkerTestTimeout):
		t.Fatal("PollAssignment did not finish")
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	if assignment.Status != "complete" || assignment.LastReport == nil || assignment.LastReport.Status != "complete" {
		t.Fatalf("assignment = %+v", assignment)
	}
}

func TestFleetWorkerRunPollsWhileCoveAssignmentRuns(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("COVE_TEST_RELEASE", release)
	store := fleetcontrol.NewMemoryStore(time.Minute)
	server := httptest.NewServer(fleetcontrol.Handler(store))
	defer server.Close()
	coveBin := writeExecutable(t, `#!/bin/sh
while [ ! -f "$COVE_TEST_RELEASE" ]; do
  sleep 0.02
done
exit 0
`)

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL:      server.URL,
		ID:                 "worker-1",
		CoveBin:            coveBin,
		HeartbeatInterval:  20 * time.Millisecond,
		AssignmentInterval: 20 * time.Millisecond,
		AssignmentTimeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		_, ok := store.Get("worker-1")
		return ok
	})
	if _, err := store.CreateAssignment(fleetcontrol.Assignment{ID: "long", WorkerID: "worker-1", Verb: "cove"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAssignment(fleetcontrol.Assignment{ID: "second", WorkerID: "worker-1", Verb: "noop"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment("long")
		return ok && assignment.Status == "running"
	})
	record, ok := store.Get("worker-1")
	if !ok {
		t.Fatal("worker missing")
	}
	seenWhileRunning := record.LastSeen
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		record, ok := store.Get("worker-1")
		return ok && record.LastSeen.After(seenWhileRunning)
	})
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment("second")
		return ok && assignment.Status == "complete"
	})
	if err := os.WriteFile(release, []byte("done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, fleetWorkerTestTimeout, func() bool {
		assignment, ok := store.GetAssignment("long")
		return ok && assignment.Status == "complete"
	})
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
	writeManifestWithDigest(t, root, "", parts...)
}

func writeManifestWithDigest(t *testing.T, root, digest string, parts ...string) {
	t.Helper()
	dir := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte("{}\n")
	if digest != "" {
		data = []byte(`{"source_manifest_digest":"` + digest + `"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0644); err != nil {
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

func waitUntil(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
