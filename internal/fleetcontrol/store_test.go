package fleetcontrol

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreHeartbeatPersistsAndSorts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "b", Host: "beta", Version: "v2", Capacity: Capacity{CPUs: 8}},
		{ID: "a", Host: "alpha", Version: "v1", Capacity: Capacity{CPUs: 4}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatalf("UpsertHeartbeat(%q): %v", hb.ID, err)
		}
	}
	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.List()
	if len(got) != 2 {
		t.Fatalf("hosts = %d, want 2", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("hosts not sorted: %+v", got)
	}
	if got[0].Capacity.CPUs != 4 || got[1].Capacity.CPUs != 8 {
		t.Fatalf("capacity not persisted: %+v", got)
	}
}

func TestStoreAuditsFleetMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.CordonWorker("worker-1", "maintenance"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.UncordonWorker("worker-1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	created, err := store.CreateAssignment(Assignment{WorkerID: "worker-1", Verb: "noop"})
	if err != nil {
		t.Fatal(err)
	}
	events := store.ListAudit(0)
	for _, want := range []string{"worker.register", "worker.cordon", "worker.uncordon", "assignment.create"} {
		if auditAction(events, want) == nil {
			t.Fatalf("audit events missing %q: %+v", want, events)
		}
	}
	event := auditAction(events, "assignment.create")
	if event.AssignmentID != created.ID || event.WorkerID != "worker-1" || event.Fields["verb"] != "noop" {
		t.Fatalf("assignment audit = %+v, want assignment %s worker-1 noop", event, created.ID)
	}
	for i, event := range events {
		if event.Hash == "" {
			t.Fatalf("audit event %d missing hash: %+v", i, event)
		}
		if i == 0 {
			if event.PrevHash != "" {
				t.Fatalf("first audit prev_hash = %q, want empty", event.PrevHash)
			}
			continue
		}
		if event.PrevHash != events[i-1].Hash {
			t.Fatalf("audit event %d prev_hash = %q, want %q", i, event.PrevHash, events[i-1].Hash)
		}
	}
	if verify := store.VerifyAudit(); !verify.OK || verify.Events != len(events) || verify.HeadHash == "" {
		t.Fatalf("VerifyAudit = %+v, want ok with %d events", verify, len(events))
	}

	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if event := auditAction(reopened.ListAudit(0), "assignment.create"); event == nil || event.AssignmentID != created.ID {
		t.Fatalf("reopened audit missing assignment create: %+v", reopened.ListAudit(0))
	}
	if verify := reopened.VerifyAudit(); !verify.OK || verify.Events != len(events) {
		t.Fatalf("reopened VerifyAudit = %+v, want ok with %d events", verify, len(events))
	}
}

func TestStoreAuditHashChainDetectsTamper(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAssignment(Assignment{WorkerID: "worker-1", Verb: "noop"}); err != nil {
		t.Fatal(err)
	}
	if verify := store.VerifyAudit(); !verify.OK {
		t.Fatalf("VerifyAudit before tamper = %+v, want ok", verify)
	}
	store.mu.Lock()
	store.audit[0].Action = "worker.tampered"
	store.mu.Unlock()
	verify := store.VerifyAudit()
	if verify.OK || len(verify.Issues) == 0 {
		t.Fatalf("VerifyAudit after tamper = %+v, want issues", verify)
	}
}

func TestStoreAuditHashChainUpgradesLegacyEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	data, err := json.Marshal(storeFile{AuditEvents: []AuditEvent{
		{ID: "audit-1", Time: now, Action: "worker.register", TargetType: "worker", TargetID: "worker-1"},
		{ID: "audit-2", Time: now.Add(time.Second), Action: "worker.cordon", TargetType: "worker", TargetID: "worker-1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if verify := store.VerifyAudit(); !verify.OK || verify.Events != 2 || verify.HeadHash == "" {
		t.Fatalf("legacy VerifyAudit = %+v, want upgraded ok", verify)
	}
	store.now = func() time.Time { return now.Add(2 * time.Second) }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if verify := reopened.VerifyAudit(); !verify.OK || verify.Events != 3 {
		t.Fatalf("reopened VerifyAudit = %+v, want ok with upgraded events", verify)
	}
	for i, event := range reopened.ListAudit(0) {
		if event.Hash == "" {
			t.Fatalf("event %d missing upgraded hash: %+v", i, event)
		}
	}
}

func TestStoreCreateSandbox(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "cold", Capacity: Capacity{VMs: 0, MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "warm", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	sandbox, err := store.CreateSandbox(SandboxRequest{
		ID:       "job-1",
		ImageRef: "base:v1",
		Args:     []string{"--net", "nat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.ID != "job-1" || sandbox.VMName != "cove-sandbox-job-1" || sandbox.WorkerID != "warm" || sandbox.Status != "pending" {
		t.Fatalf("sandbox = %+v, want job-1 on warm pending", sandbox)
	}
	wantArgs := []string{"run", "-fork-from", "base:v1", "-fork-name", "cove-sandbox-job-1", "-ephemeral", "-keep", "-headless", "--net", "nat"}
	if !equalStrings(sandbox.Assignment.Args, wantArgs) || sandbox.Assignment.SandboxID != "job-1" || sandbox.Assignment.SandboxRole != "run" {
		t.Fatalf("sandbox assignment = %+v, want args %+v", sandbox.Assignment, wantArgs)
	}
	if got, ok := store.GetSandbox("job-1"); !ok || got.ID != sandbox.ID {
		t.Fatalf("GetSandbox = %+v, %v", got, ok)
	}
	if got := store.ListSandboxes(); len(got) != 1 || got[0].ID != "job-1" {
		t.Fatalf("ListSandboxes = %+v, want job-1", got)
	}
	if event := auditAction(store.ListAudit(0), "sandbox.create"); event == nil || event.TargetID != "job-1" || event.WorkerID != "warm" {
		t.Fatalf("sandbox create audit = %+v", event)
	}
	deleted, err := store.DeleteSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Canceled || deleted.Status != "canceled" || deleted.Cleanup != nil {
		t.Fatalf("DeleteSandbox pending = %+v, want canceled without cleanup", deleted)
	}
}

func TestStoreListSandboxesFiltered(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", Labels: map[string]string{"zone": "a"}, ImageRefs: []string{"base:v1"}, Capacity: Capacity{MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-2", Labels: map[string]string{"zone": "b"}, ImageRefs: []string{"base:v2"}, Capacity: Capacity{MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	ready, err := store.CreateSandbox(SandboxRequest{Namespace: "team-a", ID: "job-ready", ImageRef: "base:v1", RequiredLabels: map[string]string{"zone": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: ready.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSandbox(SandboxRequest{Namespace: "team-a", ID: "job-pending", ImageRef: "base:v2", RequiredLabels: map[string]string{"zone": "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSandbox(SandboxRequest{Namespace: "team-b", ID: "job-other", ImageRef: "base:v1", RequiredLabels: map[string]string{"zone": "a"}}); err != nil {
		t.Fatal(err)
	}

	if got := store.ListSandboxesFiltered(SandboxListFilter{Namespace: "team-a"}); len(got) != 2 {
		t.Fatalf("team-a sandboxes = %+v, want 2", got)
	}
	if got := store.ListSandboxesFiltered(SandboxListFilter{Namespace: "team-a", Status: "ready"}); len(got) != 1 || got[0].ID != "job-ready" {
		t.Fatalf("ready sandboxes = %+v, want job-ready", got)
	}
	if got := store.ListSandboxesFiltered(SandboxListFilter{Namespace: "team-a", WorkerID: "worker-2"}); len(got) != 1 || got[0].ID != "job-pending" {
		t.Fatalf("worker-2 sandboxes = %+v, want job-pending", got)
	}
	if got := store.ListSandboxesFiltered(SandboxListFilter{Namespace: "team-a", ImageRef: "base:v2"}); len(got) != 1 || got[0].ID != "job-pending" {
		t.Fatalf("base:v2 sandboxes = %+v, want job-pending", got)
	}
	if got := store.ListSandboxesFiltered(SandboxListFilter{Namespace: "team-a", Limit: 1}); len(got) != 1 || got[0].ID != "job-ready" {
		t.Fatalf("limited sandboxes = %+v, want first team-a sandbox", got)
	}
}

func TestStoreDeleteRunningSandboxQueuesStop(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	sandbox, err := store.CreateSandbox(SandboxRequest{ID: "job-1", ImageRef: "base:v1"})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if leased == nil || leased.ID != sandbox.Assignment.ID {
		t.Fatalf("leased = %+v, want %s", leased, sandbox.Assignment.ID)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != "draining" || deleted.Cleanup == nil {
		t.Fatalf("DeleteSandbox running = %+v, want draining cleanup", deleted)
	}
	wantArgs := []string{"ctl", "-vm", "cove-sandbox-job-1", "stop"}
	if deleted.Cleanup.SandboxID != "job-1" || deleted.Cleanup.SandboxRole != "stop" || deleted.Cleanup.WorkerID != "worker-1" || !equalStrings(deleted.Cleanup.Args, wantArgs) {
		t.Fatalf("cleanup = %+v, want worker-1 args %+v", deleted.Cleanup, wantArgs)
	}
	deletedAgain, err := store.DeleteSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if deletedAgain.Cleanup == nil || deletedAgain.Cleanup.ID != deleted.Cleanup.ID {
		t.Fatalf("second DeleteSandbox cleanup = %+v, want %s", deletedAgain.Cleanup, deleted.Cleanup.ID)
	}
	cleanup, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || cleanup.ID != deleted.Cleanup.ID {
		t.Fatalf("cleanup lease = %+v, want %s", cleanup, deleted.Cleanup.ID)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: cleanup.ID, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	wait, err := store.WaitSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !wait.Done || wait.Sandbox.Status != "stopped" {
		t.Fatalf("WaitSandbox = %+v, want stopped done", wait)
	}
	now = now.Add(time.Second)
	started, err := store.StartSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !started.Started || started.Status != "pending" {
		t.Fatalf("StartSandbox stopped = %+v, want pending started", started)
	}
	wantStartArgs := []string{"run", "-vm", "cove-sandbox-job-1", "-headless"}
	if started.Assignment.WorkerID != "worker-1" || !equalStrings(started.Assignment.Args, wantStartArgs) {
		t.Fatalf("start assignment = %+v, want worker-1 args %+v", started.Assignment, wantStartArgs)
	}
	restarted, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if restarted == nil || restarted.ID != sandbox.Assignment.ID || restarted.Status != "leased" {
		t.Fatalf("restarted lease = %+v, want run assignment", restarted)
	}
	if event := auditAction(store.ListAudit(0), "sandbox.start"); event == nil || event.TargetID != "job-1" {
		t.Fatalf("sandbox start audit = %+v", event)
	}
}

func TestStoreStopSandboxPendingCancels(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSandbox(SandboxRequest{ID: "job-1", ImageRef: "base:v1"}); err != nil {
		t.Fatal(err)
	}
	stopped, err := store.StopSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !stopped.Canceled || stopped.Status != "canceled" || stopped.Cleanup != nil {
		t.Fatalf("StopSandbox pending = %+v, want canceled without cleanup", stopped)
	}
	wait, err := store.WaitSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !wait.Done || wait.Sandbox.Status != "canceled" {
		t.Fatalf("WaitSandbox = %+v, want canceled done", wait)
	}
	if event := auditAction(store.ListAudit(0), "sandbox.stop"); event == nil || event.TargetID != "job-1" {
		t.Fatalf("sandbox stop audit = %+v", event)
	}
	started, err := store.StartSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !started.Started || started.Status != "pending" {
		t.Fatalf("StartSandbox canceled = %+v, want pending started", started)
	}
	wantArgs := []string{"run", "-fork-from", "base:v1", "-fork-name", "cove-sandbox-job-1", "-ephemeral", "-keep", "-headless"}
	if !equalStrings(started.Assignment.Args, wantArgs) {
		t.Fatalf("started assignment args = %+v, want %+v", started.Assignment.Args, wantArgs)
	}
}

func TestStoreRestartRunningSandboxQueuesStopThenStart(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	sandbox, err := store.CreateSandbox(SandboxRequest{ID: "job-1", ImageRef: "base:v1", Args: []string{"--net", "nat"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	restart, err := store.RestartSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !restart.Restarting || restart.Status != "restarting" || restart.Cleanup == nil {
		t.Fatalf("RestartSandbox = %+v, want restarting cleanup", restart)
	}
	wait, err := store.WaitSandbox("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if wait.Done || wait.Sandbox.Status != "restarting" {
		t.Fatalf("WaitSandbox restarting = %+v, want not done", wait)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	got, ok := store.GetSandbox("job-1")
	if !ok || got.Status != "restarting" {
		t.Fatalf("sandbox after run completion = %+v, %v, want restarting", got, ok)
	}
	cleanup, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || cleanup.ID != restart.Cleanup.ID {
		t.Fatalf("cleanup assignment = %+v, want %s", cleanup, restart.Cleanup.ID)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: cleanup.ID, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	started, ok := store.GetSandbox("job-1")
	if !ok || started.Status != "pending" {
		t.Fatalf("sandbox after restart cleanup = %+v, %v, want pending", started, ok)
	}
	wantArgs := []string{"run", "-vm", "cove-sandbox-job-1", "-headless", "--net", "nat"}
	if !equalStrings(started.Assignment.Args, wantArgs) {
		t.Fatalf("restart start args = %+v, want %+v", started.Assignment.Args, wantArgs)
	}
	leased, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if leased == nil || leased.ID != sandbox.Assignment.ID {
		t.Fatalf("leased restarted sandbox = %+v, want %s", leased, sandbox.Assignment.ID)
	}
	if event := auditAction(store.ListAudit(0), "sandbox.restart"); event == nil || event.TargetID != "job-1" || event.Fields["restarting"] != "true" {
		t.Fatalf("sandbox restart audit = %+v", event)
	}
}

func TestStoreSandboxMeteringRecordsRunningIntervals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}}); err != nil {
		t.Fatal(err)
	}
	sandbox, err := store.CreateSandbox(SandboxRequest{
		Namespace: "team-a",
		ID:        "job-1",
		ImageRef:  "base:v1",
		Resources: Capacity{CPUs: 2, MemoryBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := store.StopSandbox("job-1"); err != nil {
		t.Fatal(err)
	}
	metering := store.ListSandboxMetering("team-a", "job-1")
	if len(metering.Records) != 2 {
		t.Fatalf("metering records = %+v, want 2", metering.Records)
	}
	if metering.Summary.DurationMillis != 4000 || metering.Summary.VMMillis != 4000 || metering.Summary.CPUMillis != 8000 || metering.Summary.MemoryByteMillis != 4096000 {
		t.Fatalf("metering summary = %+v, want 4s vm, 8 cpu-s, 4096000 byte-ms", metering.Summary)
	}
	if got := store.ListSandboxMetering("team-b", "job-1"); len(got.Records) != 0 || got.Summary.Records != 0 {
		t.Fatalf("cross-namespace metering = %+v, want none", got)
	}
	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.ListSandboxMetering("team-a", "job-1")
	if got.Summary.DurationMillis != metering.Summary.DurationMillis || len(got.Records) != len(metering.Records) {
		t.Fatalf("reopened metering = %+v, want %+v", got, metering)
	}
}

func TestStoreOperationsSummary(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CordonWorker("worker-2", "maintenance"); err != nil {
		t.Fatal(err)
	}
	sandbox, err := store.CreateSandbox(SandboxRequest{
		Namespace: "team-a",
		ID:        "job-1",
		ImageRef:  "base:v1",
		Resources: Capacity{CPUs: 2, MemoryBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.StopSandbox("job-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureWarmPool(WarmPoolRequest{Namespace: "team-a", Name: "runner", ImageRef: "base:v1", Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAssignment(Assignment{Namespace: "team-b", WorkerID: "worker-1", Verb: "noop"}); err != nil {
		t.Fatal(err)
	}

	summary := store.OperationsSummary("team-a")
	if summary.Namespace != "team-a" || summary.Workers.Total != 2 || summary.Workers.Ready != 1 || summary.Workers.Cordoned != 1 {
		t.Fatalf("workers summary = %+v", summary.Workers)
	}
	if len(summary.Workers.Attention) != 1 || summary.Workers.Attention[0].ID != "worker-2" {
		t.Fatalf("worker attention = %+v, want worker-2", summary.Workers.Attention)
	}
	if summary.Assignments.Total != 3 || summary.Assignments.Active != 3 || summary.Assignments.ByStatus["draining"] != 1 || summary.Assignments.ByStatus["pending"] != 2 {
		t.Fatalf("assignment summary = %+v, want 3 active team-a assignments", summary.Assignments)
	}
	if summary.Sandboxes.Total != 1 || summary.Sandboxes.Active != 1 || len(summary.Sandboxes.DrainingSandboxes) != 1 || summary.Sandboxes.ByStatus["draining"] != 1 {
		t.Fatalf("sandbox summary = %+v, want one draining sandbox", summary.Sandboxes)
	}
	if summary.WarmPools.Total != 1 || summary.WarmPools.Desired != 1 || summary.WarmPools.Slots != 1 || summary.WarmPools.Active != 1 || summary.WarmPools.ByStatus["pending"] != 1 {
		t.Fatalf("warm-pool summary = %+v, want one pending slot", summary.WarmPools)
	}
	if summary.Metering.Namespace != "team-a" || summary.Metering.Records == 0 || summary.Metering.DurationMillis == 0 {
		t.Fatalf("metering summary = %+v, want team-a usage", summary.Metering)
	}
	for _, assignment := range summary.Assignments.ActiveAssignments {
		if assignment.Namespace != "team-a" {
			t.Fatalf("active assignment namespace = %q, want team-a", assignment.Namespace)
		}
	}
}

func TestStoreSandboxExecQueuesSameWorkerShell(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}}); err != nil {
		t.Fatal(err)
	}
	sandbox, err := store.CreateSandbox(SandboxRequest{ID: "job-1", ImageRef: "base:v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	result, err := store.ExecSandbox("job-1", SandboxExecRequest{
		Command: []string{"/bin/echo", "ok"},
		Env:     map[string]string{"B": "2", "A": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Done {
		t.Fatalf("exec result Done = true, want false")
	}
	if result.Assignment.WorkerID != "worker-1" || result.Assignment.SandboxID != "job-1" || result.Assignment.SandboxRole != sandboxRoleExec {
		t.Fatalf("exec assignment = %+v, want same-worker sandbox exec", result.Assignment)
	}
	wantArgs := []string{"shell", "--env", "A=1", "--env", "B=2", "cove-sandbox-job-1", "--", "/bin/echo", "ok"}
	if got := strings.Join(result.Assignment.Args, " "); got != strings.Join(wantArgs, " ") {
		t.Fatalf("exec args = %q, want %q", got, strings.Join(wantArgs, " "))
	}
	leased, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if leased.ID != result.Assignment.ID {
		t.Fatalf("leased exec assignment = %+v, want %s", leased, result.Assignment.ID)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: result.Assignment.ID, Status: "complete", ExitCode: 0, Stdout: "ok\n"}); err != nil {
		t.Fatal(err)
	}
	finished, ok := store.GetAssignment(result.Assignment.ID)
	if !ok {
		t.Fatal("exec assignment missing")
	}
	done := sandboxExecResult("job-1", "cove-sandbox-job-1", finished)
	if !done.Done || done.ExitCode != 0 || done.Stdout != "ok\n" {
		t.Fatalf("finished exec = %+v, want done ok", done)
	}
}

func TestStoreSandboxControlQueuesSameWorkerControl(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}}); err != nil {
		t.Fatal(err)
	}
	sandbox, err := store.CreateSandbox(SandboxRequest{ID: "job-1", ImageRef: "base:v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: sandbox.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	result, err := store.ControlSandbox("job-1", SandboxControlRequest{
		Type:       "screenshot",
		Screenshot: map[string]any{"format": "png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Done {
		t.Fatalf("control result Done = true, want false")
	}
	if result.Assignment.WorkerID != "worker-1" || result.Assignment.SandboxID != "job-1" || result.Assignment.SandboxRole != sandboxRoleControl || result.Assignment.Verb != "cove-control" {
		t.Fatalf("control assignment = %+v, want same-worker sandbox control", result.Assignment)
	}
	if len(result.Assignment.Args) != 2 || result.Assignment.Args[0] != "cove-sandbox-job-1" {
		t.Fatalf("control args = %+v, want vm name and json payload", result.Assignment.Args)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Assignment.Args[1]), &payload); err != nil {
		t.Fatalf("decode control payload: %v", err)
	}
	if payload["type"] != "screenshot" {
		t.Fatalf("control payload = %+v, want screenshot", payload)
	}
	screenshot, ok := payload["screenshot"].(map[string]any)
	if !ok || screenshot["format"] != "png" {
		t.Fatalf("screenshot payload = %+v, want format png", payload["screenshot"])
	}
	leased, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if leased.ID != result.Assignment.ID {
		t.Fatalf("leased control assignment = %+v, want %s", leased, result.Assignment.ID)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: result.Assignment.ID, Status: "complete", ExitCode: 0, Stdout: `{"success":true,"screenshot_result":{"image_data":"cG5n"}}`}); err != nil {
		t.Fatal(err)
	}
	finished, ok := store.GetAssignment(result.Assignment.ID)
	if !ok {
		t.Fatal("control assignment missing")
	}
	done := sandboxControlResult("job-1", "cove-sandbox-job-1", "screenshot", finished)
	if !done.Done || done.ExitCode != 0 || done.Data != "cG5n" {
		t.Fatalf("finished control = %+v, want done image data", done)
	}
}

func TestStoreSandboxLeaseAcquireRenewRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSandbox(SandboxRequest{ID: "job-1", ImageRef: "base:v1"}); err != nil {
		t.Fatal(err)
	}
	leased, err := store.LeaseSandbox("job-1", SandboxLeaseRequest{Holder: "client-a", TTL: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	if leased.Lease.Holder != "client-a" || !leased.Lease.Expires.Equal(now.Add(time.Second)) {
		t.Fatalf("lease = %+v, want client-a expiring in 1s", leased.Lease)
	}
	if leased.Sandbox.Lease == nil || leased.Sandbox.Assignment.SandboxLeaseHolder != "client-a" {
		t.Fatalf("leased sandbox = %+v, want visible lease", leased.Sandbox)
	}

	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = func() time.Time { return now }
	got, ok := reopened.GetSandbox("job-1")
	if !ok || got.Lease == nil || got.Lease.Holder != "client-a" {
		t.Fatalf("reopened sandbox = %+v, %v, want client-a lease", got, ok)
	}

	now = now.Add(500 * time.Millisecond)
	renewed, err := store.LeaseSandbox("job-1", SandboxLeaseRequest{Holder: "client-a", TTL: "2s"})
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.Lease.Expires.Equal(now.Add(2 * time.Second)) {
		t.Fatalf("renewed lease expires = %v, want %v", renewed.Lease.Expires, now.Add(2*time.Second))
	}
	if _, err := store.LeaseSandbox("job-1", SandboxLeaseRequest{Holder: "client-b"}); err == nil || !strings.Contains(err.Error(), "lease held by") {
		t.Fatalf("conflicting LeaseSandbox err = %v, want held-by error", err)
	}
	if _, err := store.ReleaseSandboxLease("job-1", "client-b"); err == nil || !strings.Contains(err.Error(), "lease held by") {
		t.Fatalf("wrong release err = %v, want held-by error", err)
	}

	now = now.Add(3 * time.Second)
	leased, err = store.LeaseSandbox("job-1", SandboxLeaseRequest{Holder: "client-b"})
	if err != nil {
		t.Fatal(err)
	}
	if leased.Lease.Holder != "client-b" {
		t.Fatalf("expired lease takeover = %+v, want client-b", leased.Lease)
	}
	released, err := store.ReleaseSandboxLease("job-1", "client-b")
	if err != nil {
		t.Fatal(err)
	}
	if released.Lease != nil || released.Assignment.SandboxLeaseHolder != "" || !released.Assignment.SandboxLeaseExpires.IsZero() {
		t.Fatalf("released sandbox = %+v, want no lease", released)
	}
	if event := auditAction(store.ListAudit(0), "sandbox.lease"); event == nil || event.TargetID != "job-1" || event.Fields["holder"] == "" {
		t.Fatalf("sandbox lease audit = %+v", event)
	}
	if event := auditAction(store.ListAudit(0), "sandbox.lease.release"); event == nil || event.TargetID != "job-1" || event.Fields["holder"] != "client-b" {
		t.Fatalf("sandbox lease release audit = %+v", event)
	}
}

func TestStoreServiceAccountsPersistTokenHashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.UpsertServiceAccount(ServiceAccountRequest{Name: "ci", Token: "secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ServiceAccount.Name != "ci" {
		t.Fatalf("service account = %+v, want ci", result.ServiceAccount)
	}
	if result.ServiceAccount.Role != ServiceAccountRoleAdmin {
		t.Fatalf("service account role = %q, want admin", result.ServiceAccount.Role)
	}
	if _, ok := store.AuthenticateServiceAccount("secret-token"); !ok {
		t.Fatal("AuthenticateServiceAccount(secret-token) = false")
	}
	if _, ok := store.AuthenticateServiceAccount("wrong"); ok {
		t.Fatal("AuthenticateServiceAccount(wrong) = true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret-token")) {
		t.Fatalf("store file contains plaintext token:\n%s", data)
	}

	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if accounts := reopened.ListServiceAccounts(); len(accounts) != 1 || accounts[0].Name != "ci" {
		t.Fatalf("accounts = %+v, want ci", accounts)
	}
	if _, ok := reopened.AuthenticateServiceAccount("secret-token"); !ok {
		t.Fatal("reopened AuthenticateServiceAccount(secret-token) = false")
	}
}

func TestStoreOIDCBindingAuthenticatesRS256JWT(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	key, keyPEM := testOIDCKey(t)
	path := filepath.Join(t.TempDir(), "fleet.json")
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	result, err := store.UpsertOIDCBinding(OIDCBindingRequest{
		Name:      "okta-ci",
		Issuer:    "https://issuer.example",
		Subject:   "repo:tmc/cove:ref:main",
		Audience:  "cove-fleet",
		Namespace: "team-a",
		Role:      ServiceAccountRoleOperator,
		Keys:      []OIDCKey{{KID: "kid-1", PEM: keyPEM}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding.Name != "okta-ci" || result.Binding.Role != ServiceAccountRoleOperator || len(result.Binding.KeyIDs) != 1 || result.Binding.KeyIDs[0] != "kid-1" {
		t.Fatalf("binding = %+v", result.Binding)
	}
	if _, err := store.UpsertOIDCBinding(OIDCBindingRequest{
		Name:     "missing-role",
		Issuer:   "https://issuer.example",
		Subject:  "repo:tmc/cove:ref:main",
		Audience: "cove-fleet",
		Keys:     []OIDCKey{{PEM: keyPEM}},
	}); err == nil || !strings.Contains(err.Error(), "role required") {
		t.Fatalf("missing role err = %v, want role required", err)
	}
	token := signOIDCJWT(t, key, "kid-1", map[string]any{
		"iss": "https://issuer.example",
		"sub": "repo:tmc/cove:ref:main",
		"aud": []string{"cove-fleet", "other"},
		"exp": now.Add(time.Hour).Unix(),
	})
	principal, ok := store.AuthenticateBearer(token)
	if !ok {
		t.Fatal("AuthenticateBearer(oidc token) = false")
	}
	if principal.Actor != "oidc:okta-ci" || principal.Namespace != "team-a" || principal.Role != ServiceAccountRoleOperator {
		t.Fatalf("principal = %+v, want oidc okta-ci team-a operator", principal)
	}
	wrongAudience := signOIDCJWT(t, key, "kid-1", map[string]any{
		"iss": "https://issuer.example",
		"sub": "repo:tmc/cove:ref:main",
		"aud": "wrong",
		"exp": now.Add(time.Hour).Unix(),
	})
	if _, ok := store.AuthenticateBearer(wrongAudience); ok {
		t.Fatal("AuthenticateBearer(wrong audience) = true")
	}
	expired := signOIDCJWT(t, key, "kid-1", map[string]any{
		"iss": "https://issuer.example",
		"sub": "repo:tmc/cove:ref:main",
		"aud": "cove-fleet",
		"exp": now.Add(-2 * time.Minute).Unix(),
	})
	if _, ok := store.AuthenticateBearer(expired); ok {
		t.Fatal("AuthenticateBearer(expired token) = true")
	}

	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = func() time.Time { return now }
	if principal, ok := reopened.AuthenticateBearer(token); !ok || principal.Actor != "oidc:okta-ci" {
		t.Fatalf("reopened AuthenticateBearer = %+v, %v", principal, ok)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("PRIVATE KEY")) {
		t.Fatalf("store file contains private key:\n%s", data)
	}
}

func TestStoreOIDCBindingDiscoversJWKS(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	key, _ := testOIDCKey(t)
	var discoveryRequests int32
	var jwksRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			atomic.AddInt32(&discoveryRequests, 1)
			writeTestJSON(w, map[string]any{
				"issuer":   "http://" + r.Host,
				"jwks_uri": "http://" + r.Host + "/jwks",
			})
		case "/jwks":
			atomic.AddInt32(&jwksRequests, 1)
			writeTestJSON(w, map[string]any{"keys": []any{testOIDCJWK(key, "kid-1")}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "fleet.json")
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	result, err := store.UpsertOIDCBinding(OIDCBindingRequest{
		Name:      "github-main",
		Issuer:    server.URL,
		Subject:   "repo:tmc/cove:ref:refs/heads/main",
		Audience:  "cove-fleet",
		Namespace: "team-a",
		Role:      ServiceAccountRoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding.JWKSURL != "" || len(result.Binding.KeyIDs) != 0 {
		t.Fatalf("binding = %+v, want discovery pending without cached keys", result.Binding)
	}
	token := signOIDCJWT(t, key, "kid-1", map[string]any{
		"iss": server.URL,
		"sub": "repo:tmc/cove:ref:refs/heads/main",
		"aud": "cove-fleet",
		"exp": now.Add(time.Hour).Unix(),
	})
	principal, ok := store.AuthenticateBearer(token)
	if !ok {
		t.Fatal("AuthenticateBearer(discovered oidc token) = false")
	}
	if principal.Actor != "oidc:github-main" || principal.Namespace != "team-a" || principal.Role != ServiceAccountRoleOperator {
		t.Fatalf("principal = %+v, want oidc github-main team-a operator", principal)
	}
	if atomic.LoadInt32(&discoveryRequests) != 1 || atomic.LoadInt32(&jwksRequests) != 1 {
		t.Fatalf("requests discovery=%d jwks=%d, want 1/1", discoveryRequests, jwksRequests)
	}
	bindings := store.ListOIDCBindings()
	if len(bindings) != 1 || bindings[0].JWKSURL != server.URL+"/jwks" || len(bindings[0].KeyIDs) != 1 || bindings[0].KeyIDs[0] != "kid-1" || bindings[0].JWKSFetched.IsZero() {
		t.Fatalf("bindings = %+v, want cached jwks key", bindings)
	}
	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = func() time.Time { return now }
	server.Close()
	if _, ok := reopened.AuthenticateBearer(token); !ok {
		t.Fatal("reopened AuthenticateBearer(discovered oidc token) = false")
	}
}

func TestStoreOIDCBindingRefreshesJWKSOnKeyMiss(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	oldKey, oldKeyPEM := testOIDCKey(t)
	newKey, _ := testOIDCKey(t)
	var jwksRequests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&jwksRequests, 1)
		writeTestJSON(w, map[string]any{"keys": []any{testOIDCJWK(newKey, "kid-2")}})
	}))
	defer server.Close()
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertOIDCBinding(OIDCBindingRequest{
		Name:      "okta-ci",
		Issuer:    "https://issuer.example",
		Subject:   "repo:tmc/cove:ref:main",
		Audience:  "cove-fleet",
		Namespace: "team-a",
		Role:      ServiceAccountRoleOperator,
		JWKSURL:   server.URL,
		Keys:      []OIDCKey{{KID: "kid-1", PEM: oldKeyPEM}},
	}); err != nil {
		t.Fatal(err)
	}
	token := signOIDCJWT(t, newKey, "kid-2", map[string]any{
		"iss": "https://issuer.example",
		"sub": "repo:tmc/cove:ref:main",
		"aud": "cove-fleet",
		"exp": now.Add(time.Hour).Unix(),
	})
	principal, ok := store.AuthenticateBearer(token)
	if !ok {
		t.Fatal("AuthenticateBearer(rotated oidc token) = false")
	}
	if principal.Actor != "oidc:okta-ci" {
		t.Fatalf("principal = %+v, want oidc:okta-ci", principal)
	}
	if atomic.LoadInt32(&jwksRequests) != 1 {
		t.Fatalf("jwks requests = %d, want 1", jwksRequests)
	}
	bindings := store.ListOIDCBindings()
	if len(bindings) != 1 || len(bindings[0].KeyIDs) != 1 || bindings[0].KeyIDs[0] != "kid-2" {
		t.Fatalf("bindings = %+v, want refreshed kid-2", bindings)
	}
	oldToken := signOIDCJWT(t, oldKey, "kid-1", map[string]any{
		"iss": "https://issuer.example",
		"sub": "repo:tmc/cove:ref:main",
		"aud": "cove-fleet",
		"exp": now.Add(time.Hour).Unix(),
	})
	if _, ok := store.AuthenticateBearer(oldToken); ok {
		t.Fatal("AuthenticateBearer(old oidc token) = true")
	}
}

func TestStoreMarksStaleAfterTTL(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now.Add(2 * time.Minute) }
	got, ok := store.Get("worker-1")
	if !ok {
		t.Fatal("worker missing")
	}
	if got.Status != "stale" {
		t.Fatalf("status = %q, want stale", got.Status)
	}
}

func TestStoreReportRequiresRegisteredWorker(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.Report(WorkerReport{ID: "missing", Status: "done"}); err == nil {
		t.Fatal("Report() error = nil, want missing worker")
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: "a1", Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Report == nil || got.Report.AssignmentID != "a1" || got.Status != "ready" {
		t.Fatalf("report not recorded: %+v", got)
	}
}

func TestStoreAssignmentsLeaseReportAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateAssignment(Assignment{
		ID:       "assignment-1",
		WorkerID: "worker-1",
		Verb:     "noop",
		Args:     []string{"arg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "pending" || created.Created.IsZero() || created.Updated.IsZero() {
		t.Fatalf("created assignment = %+v", created)
	}
	got, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "assignment-1" || got.Status != "leased" || got.LeasedTo != "worker-1" || got.LeaseExpires.IsZero() {
		t.Fatalf("leased assignment = %+v", got)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: "assignment-1", Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reopenedAssignment, ok := reopened.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing after reopen")
	}
	if reopenedAssignment.Status != "complete" || reopenedAssignment.LastReport == nil || reopenedAssignment.LastReport.Status != "complete" {
		t.Fatalf("reopened assignment = %+v", reopenedAssignment)
	}
	if event := auditAction(reopened.ListAudit(0), "assignment.report"); event == nil || event.AssignmentID != "assignment-1" || event.Status != "complete" {
		t.Fatalf("reopened audit missing terminal report: %+v", reopened.ListAudit(0))
	}
}

func TestStoreReportRenewsRunningAssignment(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.assignmentTTL = 2 * time.Second
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAssignment(Assignment{ID: "assignment-1", WorkerID: "worker-1", Verb: "cove"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	reportTime := now.Add(time.Second)
	store.now = func() time.Time { return reportTime }
	record, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: "assignment-1", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "ready" || !record.Expires.Equal(reportTime.Add(time.Minute)) {
		t.Fatalf("record = %+v", record)
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	if assignment.Status != "running" || assignment.LeasedTo != "worker-1" || !assignment.LeaseExpires.Equal(reportTime.Add(2*time.Second)) {
		t.Fatalf("assignment = %+v", assignment)
	}
}

func TestStoreAssignmentLeaseExpires(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.assignmentTTL = time.Second
	store.now = func() time.Time { return now }
	for _, id := range []string{"worker-1", "worker-2"} {
		if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateAssignment(Assignment{ID: "assignment-1", Verb: "noop"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.LeasedTo != "worker-1" {
		t.Fatalf("first lease = %+v", got)
	}
	store.now = func() time.Time { return now.Add(500 * time.Millisecond) }
	got, err = store.AwaitAssignment("worker-2")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("assignment before expiry = %+v, want nil", got)
	}
	store.now = func() time.Time { return now.Add(2 * time.Second) }
	got, err = store.AwaitAssignment("worker-2")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.LeasedTo != "worker-2" {
		t.Fatalf("expired lease reassignment = %+v", got)
	}
}

func TestStoreReconcileRequeuesExpiredLease(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.assignmentTTL = time.Second
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAssignment(Assignment{ID: "assignment-1", Verb: "noop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now.Add(2 * time.Second) }
	result, err := store.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RequeuedAssignments) != 1 || result.RequeuedAssignments[0] != "assignment-1" {
		t.Fatalf("reconcile result = %+v", result)
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	if assignment.Status != "pending" || assignment.LeasedTo != "" || !assignment.LeaseExpires.IsZero() {
		t.Fatalf("assignment = %+v", assignment)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: "assignment-1", Status: "complete"}); err == nil {
		t.Fatal("late Report() error = nil, want lease error")
	}
}

func TestStoreReconcileReplacesStaleScheduledWorker(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.assignmentTTL = 10 * time.Minute
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{
		ID:        "warm",
		ImageRefs: []string{"macos-runner:latest"},
		Capacity:  Capacity{VMs: 4},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "cold", Capacity: Capacity{VMs: 1}}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateAssignment(Assignment{
		ID:       "assignment-1",
		Policy:   PolicyImageAffinity,
		ImageRef: "macos-runner:latest",
		Verb:     "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkerID != "warm" {
		t.Fatalf("created WorkerID = %q, want warm", created.WorkerID)
	}
	if _, err := store.AwaitAssignment("warm"); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now.Add(30 * time.Second) }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "cold", Capacity: Capacity{VMs: 1}}); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now.Add(70 * time.Second) }
	result, err := store.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.StaleWorkers) != 1 || result.StaleWorkers[0] != "warm" {
		t.Fatalf("stale workers = %+v", result)
	}
	if len(result.ReplacedAssignments) != 1 || result.ReplacedAssignments[0] != "assignment-1" {
		t.Fatalf("replaced assignments = %+v", result)
	}
	assignment, ok := store.GetAssignment("assignment-1")
	if !ok {
		t.Fatal("assignment missing")
	}
	if assignment.Status != "pending" || assignment.WorkerID != "cold" || assignment.LeasedTo != "" {
		t.Fatalf("assignment = %+v", assignment)
	}
}

func TestStoreSchedulesLeastLoadedAssignment(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "busy", Capacity: Capacity{VMs: 4}},
		{ID: "idle", Capacity: Capacity{VMs: 1}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	assignment, err := store.CreateAssignment(Assignment{
		ID:     "assignment-1",
		Policy: PolicyLeastLoaded,
		Verb:   "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "idle" {
		t.Fatalf("WorkerID = %q, want idle", assignment.WorkerID)
	}
}

func TestStoreSchedulesImageAffinityAssignment(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "warm", ImageRefs: []string{"macos-runner:latest"}, Capacity: Capacity{VMs: 4}},
		{ID: "cold", Capacity: Capacity{VMs: 1}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	assignment, err := store.CreateAssignment(Assignment{
		ID:       "assignment-1",
		Policy:   PolicyImageAffinity,
		ImageRef: "macos-runner:latest",
		Verb:     "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "warm" {
		t.Fatalf("WorkerID = %q, want warm", assignment.WorkerID)
	}
}

func TestStoreSchedulesBinPackAssignment(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "low", Capacity: Capacity{VMs: 1, MaxVMs: 4}},
		{ID: "dense", Capacity: Capacity{VMs: 3, MaxVMs: 4}},
		{ID: "full", Capacity: Capacity{VMs: 4, MaxVMs: 4}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	assignment, err := store.CreateAssignment(Assignment{
		ID:     "assignment-1",
		Policy: PolicyBinPack,
		Verb:   "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "dense" {
		t.Fatalf("WorkerID = %q, want dense", assignment.WorkerID)
	}

	assignment, err = store.CreateAssignment(Assignment{
		ID:        "assignment-2",
		Policy:    PolicyBinPack,
		Resources: Capacity{VMs: 2},
		Verb:      "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "low" {
		t.Fatalf("WorkerID = %q, want low", assignment.WorkerID)
	}
	if assignment.Resources.VMs != 2 {
		t.Fatalf("resources = %+v, want VMs 2", assignment.Resources)
	}
}

func TestStoreSchedulesBinPackWithAntiAffinity(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "dense", Capacity: Capacity{VMs: 2, MaxVMs: 5}},
		{ID: "open", Capacity: Capacity{VMs: 1, MaxVMs: 5}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateAssignment(Assignment{
		ID:              "existing",
		WorkerID:        "dense",
		AntiAffinityKey: "job-a",
		Verb:            "cove",
	}); err != nil {
		t.Fatal(err)
	}
	assignment, err := store.CreateAssignment(Assignment{
		ID:              "assignment-1",
		Policy:          PolicyBinPack,
		AntiAffinityKey: "job-a",
		Verb:            "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "open" {
		t.Fatalf("WorkerID = %q, want open", assignment.WorkerID)
	}
}

func TestStorePlansPlacementCandidates(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "dense", Labels: map[string]string{"zone": "desk"}, ImageRefs: []string{"macos-runner:latest"}, Capacity: Capacity{VMs: 3, MaxVMs: 5}},
		{ID: "open", Labels: map[string]string{"zone": "desk"}, Capacity: Capacity{VMs: 2, MaxVMs: 5}},
		{ID: "full", Labels: map[string]string{"zone": "desk"}, Capacity: Capacity{VMs: 5, MaxVMs: 5}},
		{ID: "rack", Labels: map[string]string{"zone": "rack"}, Capacity: Capacity{VMs: 0, MaxVMs: 5}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateAssignment(Assignment{
		ID:              "existing",
		WorkerID:        "dense",
		AntiAffinityKey: "job-a",
		Resources:       Capacity{VMs: 1},
		Verb:            "cove",
	}); err != nil {
		t.Fatal(err)
	}
	request := Assignment{
		Policy:          PolicyBinPack,
		ImageRef:        "macos-runner:latest",
		RequiredLabels:  map[string]string{"zone": "desk"},
		AntiAffinityKey: "job-a",
		Resources:       Capacity{VMs: 1},
		Verb:            "cove",
	}
	plan, err := store.PlanAssignment(request, 2)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Policy != PolicyBinPack || plan.ImageRef != "macos-runner:latest" || plan.Resources.VMs != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Candidates) != 2 {
		t.Fatalf("candidates = %+v, want 2", plan.Candidates)
	}
	if got := plan.Candidates[0]; got.Rank != 1 || got.WorkerID != "open" || got.Load != 2 || got.RequestedVMs != 1 || got.AntiAffinityLoad != 0 {
		t.Fatalf("first candidate = %+v, want open", got)
	}
	if got := plan.Candidates[1]; got.Rank != 2 || got.WorkerID != "dense" || got.Load != 4 || got.RequestedVMs != 1 || got.AntiAffinityLoad != 1 || !got.HasImage {
		t.Fatalf("second candidate = %+v, want dense", got)
	}
	created, err := store.CreateAssignment(request)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkerID != plan.Candidates[0].WorkerID {
		t.Fatalf("CreateAssignment WorkerID = %q, want plan first candidate %q", created.WorkerID, plan.Candidates[0].WorkerID)
	}
}

func TestStoreImageAffinityPrefersWarmBeforeAntiAffinity(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "warm", ImageRefs: []string{"macos-runner:latest"}, Capacity: Capacity{VMs: 1, MaxVMs: 5}},
		{ID: "cold", Capacity: Capacity{VMs: 0, MaxVMs: 5}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateAssignment(Assignment{
		ID:              "existing",
		WorkerID:        "warm",
		AntiAffinityKey: "job-a",
		Verb:            "cove",
	}); err != nil {
		t.Fatal(err)
	}
	assignment, err := store.CreateAssignment(Assignment{
		ID:              "assignment-1",
		Policy:          PolicyImageAffinity,
		ImageRef:        "macos-runner:latest",
		AntiAffinityKey: "job-a",
		Verb:            "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "warm" {
		t.Fatalf("WorkerID = %q, want warm", assignment.WorkerID)
	}
}

func TestStoreSchedulesWithRequiredLabelsAndPendingLoad(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "a", Labels: map[string]string{"zone": "desk"}, Capacity: Capacity{VMs: 1}},
		{ID: "b", Labels: map[string]string{"zone": "desk"}, Capacity: Capacity{VMs: 1}},
		{ID: "c", Labels: map[string]string{"zone": "rack"}, Capacity: Capacity{VMs: 0}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateAssignment(Assignment{ID: "assignment-1", WorkerID: "a", Verb: "cove"}); err != nil {
		t.Fatal(err)
	}
	assignment, err := store.CreateAssignment(Assignment{
		ID:             "assignment-2",
		Policy:         PolicyLeastLoaded,
		RequiredLabels: map[string]string{"zone": "desk"},
		Verb:           "cove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "b" {
		t.Fatalf("WorkerID = %q, want b", assignment.WorkerID)
	}
}

func TestStoreCordonPersistsAcrossHeartbeatAndSkipsPlacement(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "drain", Capacity: Capacity{VMs: 0}},
		{ID: "ready", Capacity: Capacity{VMs: 5}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	record, err := store.CordonWorker("drain", "maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if !record.Cordoned || record.Status != "cordoned" || record.CordonReason != "maintenance" || record.CordonedAt.IsZero() {
		t.Fatalf("cordoned record = %+v", record)
	}
	now = now.Add(10 * time.Second)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "drain", Capacity: Capacity{VMs: 0}}); err != nil {
		t.Fatal(err)
	}
	record, ok := store.Get("drain")
	if !ok {
		t.Fatal("worker missing")
	}
	if !record.Cordoned || record.Status != "cordoned" || record.CordonReason != "maintenance" {
		t.Fatalf("heartbeat cleared cordon: %+v", record)
	}
	placed, err := store.CreateAssignment(Assignment{
		ID:     "assignment-1",
		Policy: PolicyLeastLoaded,
		Verb:   "noop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if placed.WorkerID != "ready" {
		t.Fatalf("WorkerID = %q, want ready", placed.WorkerID)
	}
	if _, err := store.CreateAssignment(Assignment{
		ID:       "assignment-2",
		WorkerID: "drain",
		Verb:     "noop",
	}); err != nil {
		t.Fatal(err)
	}
	leased, err := store.AwaitAssignment("drain")
	if err != nil {
		t.Fatal(err)
	}
	if leased == nil || leased.ID != "assignment-2" {
		t.Fatalf("direct lease = %+v, want assignment-2", leased)
	}
}

func TestStoreDrainWorkerCordonsAndStopsSandboxes(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "drain", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	ready, err := store.CreateSandbox(SandboxRequest{ID: "job-ready", ImageRef: "base:v1"})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := store.AwaitAssignment("drain")
	if err != nil {
		t.Fatal(err)
	}
	if leased == nil || leased.ID != ready.Assignment.ID {
		t.Fatalf("leased = %+v, want %s", leased, ready.Assignment.ID)
	}
	now = now.Add(time.Second)
	if _, err := store.Report(WorkerReport{ID: "drain", AssignmentID: ready.Assignment.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.CreateSandbox(SandboxRequest{ID: "job-pending", ImageRef: "base:v1"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	result, err := store.DrainWorker("drain", "maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Worker.Cordoned || result.Worker.Status != "cordoned" || result.Worker.CordonReason != "maintenance" {
		t.Fatalf("drained worker = %+v, want cordoned maintenance", result.Worker)
	}
	if len(result.Sandboxes) != 2 {
		t.Fatalf("drain sandboxes = %+v, want 2", result.Sandboxes)
	}
	byID := make(map[string]SandboxStopResult)
	for _, stopped := range result.Sandboxes {
		byID[stopped.ID] = stopped
	}
	if got := byID["job-ready"]; got.Status != "draining" || got.Cleanup == nil || got.Cleanup.WorkerID != "drain" {
		t.Fatalf("job-ready drain = %+v, want draining cleanup on worker", got)
	}
	if got := byID["job-pending"]; got.Status != "canceled" || !got.Canceled || got.Cleanup != nil {
		t.Fatalf("job-pending drain = %+v, want canceled without cleanup", got)
	}
	if got, ok := store.GetSandbox("job-ready"); !ok || got.Status != "draining" {
		t.Fatalf("GetSandbox(job-ready) = %+v, %v; want draining", got, ok)
	}
	if got, ok := store.GetSandbox("job-pending"); !ok || got.Status != "canceled" || got.Assignment.ID != pending.Assignment.ID {
		t.Fatalf("GetSandbox(job-pending) = %+v, %v; want canceled", got, ok)
	}
	if event := auditAction(store.ListAudit(0), "worker.drain"); event == nil || event.WorkerID != "drain" || event.Fields["sandboxes"] != "2" {
		t.Fatalf("worker drain audit = %+v", event)
	}
	if event := auditAction(store.ListAudit(0), "sandbox.drain"); event == nil || event.WorkerID != "drain" {
		t.Fatalf("sandbox drain audit = %+v", event)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "ready", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	next, err := store.CreateSandbox(SandboxRequest{ID: "job-next", ImageRef: "base:v1"})
	if err != nil {
		t.Fatal(err)
	}
	if next.WorkerID != "ready" {
		t.Fatalf("post-drain sandbox worker = %q, want ready", next.WorkerID)
	}
}

func TestStoreUncordonRestoresPlacement(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, id := range []string{"a", "b"} {
		if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CordonWorker("a", "maintenance"); err != nil {
		t.Fatal(err)
	}
	record, err := store.UncordonWorker("a")
	if err != nil {
		t.Fatal(err)
	}
	if record.Cordoned || record.Status != "ready" || record.CordonReason != "" || !record.CordonedAt.IsZero() {
		t.Fatalf("uncordoned record = %+v", record)
	}
	assignment, err := store.CreateAssignment(Assignment{
		ID:     "assignment-1",
		Policy: PolicyLeastLoaded,
		Verb:   "noop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.WorkerID != "a" {
		t.Fatalf("WorkerID = %q, want a", assignment.WorkerID)
	}
}

func TestStorePrepareImageCreatesMissingWorkerAssignments(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	for _, hb := range []WorkerHeartbeat{
		{ID: "warm", Labels: map[string]string{"zone": "desk"}, ImageRefs: []string{"macos-runner:latest"}},
		{ID: "cold", Labels: map[string]string{"zone": "desk"}},
		{ID: "drain", Labels: map[string]string{"zone": "desk"}},
		{ID: "rack", Labels: map[string]string{"zone": "rack"}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CordonWorker("drain", "maintenance"); err != nil {
		t.Fatal(err)
	}
	result, err := store.PrepareImage(ImagePrepareRequest{
		SourceRef:      "registry.example/cove/macos-runner:latest",
		ImageRef:       "macos-runner:latest",
		RequiredLabels: map[string]string{"zone": "desk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 1 {
		t.Fatalf("assignments = %+v, want 1", result.Assignments)
	}
	assignment := result.Assignments[0]
	if assignment.WorkerID != "cold" || assignment.Verb != "cove" || assignment.ImageRef != "macos-runner:latest" {
		t.Fatalf("assignment = %+v", assignment)
	}
	wantArgs := []string{"image", "pull", "-tag", "macos-runner:latest", "registry.example/cove/macos-runner:latest"}
	if !equalStrings(assignment.Args, wantArgs) {
		t.Fatalf("args = %+v, want %+v", assignment.Args, wantArgs)
	}
	if skipReason(result.Skipped, "warm") != "present" || skipReason(result.Skipped, "drain") != "cordoned" || skipReason(result.Skipped, "rack") != "" {
		t.Fatalf("skipped = %+v", result.Skipped)
	}

	result, err = store.PrepareImage(ImagePrepareRequest{
		SourceRef:      "registry.example/cove/macos-runner:latest",
		ImageRef:       "macos-runner:latest",
		RequiredLabels: map[string]string{"zone": "desk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 0 || skipReason(result.Skipped, "cold") != "active" {
		t.Fatalf("second prepare result = %+v", result)
	}
}

func TestStorePushesImageGCAssignments(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "desk", Labels: map[string]string{"zone": "desk"}},
		{ID: "rack", Labels: map[string]string{"zone": "rack"}},
		{ID: "drain", Labels: map[string]string{"zone": "desk"}},
		{ID: "stale", Labels: map[string]string{"zone": "desk"}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CordonWorker("drain", "maintenance"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "desk", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "rack", Labels: map[string]string{"zone": "rack"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "drain", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.PushImageGC(ImageGCRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		OlderThan:      "24h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 1 {
		t.Fatalf("assignments = %+v, want 1", result.Assignments)
	}
	assignment := result.Assignments[0]
	wantArgs := []string{"image", "gc", "-dry-run", "-older-than", "24h"}
	if assignment.WorkerID != "desk" || assignment.Verb != "cove" || !equalStrings(assignment.Args, wantArgs) {
		t.Fatalf("assignment = %+v, want worker desk args %+v", assignment, wantArgs)
	}
	if skipImageGCReason(result.Skipped, "drain") != "cordoned" || skipImageGCReason(result.Skipped, "stale") != "stale" || skipImageGCReason(result.Skipped, "rack") != "" {
		t.Fatalf("skipped = %+v", result.Skipped)
	}

	result, err = store.PushImageGC(ImageGCRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		OlderThan:      "24h",
		Apply:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 0 || skipImageGCReason(result.Skipped, "desk") != "active" {
		t.Fatalf("second image gc = %+v, want active skip for desk", result)
	}
}

func TestStorePushesLifecyclePolicyAssignments(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "desk", Labels: map[string]string{"zone": "desk"}},
		{ID: "rack", Labels: map[string]string{"zone": "rack"}},
		{ID: "drain", Labels: map[string]string{"zone": "desk"}},
		{ID: "stale", Labels: map[string]string{"zone": "desk"}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CordonWorker("drain", "maintenance"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "desk", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "rack", Labels: map[string]string{"zone": "rack"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "drain", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.PushLifecyclePolicy(LifecyclePolicyRequest{
		VMName:         "ci-runner",
		RequiredLabels: map[string]string{"zone": "desk"},
		IdleTimeout:    "30m",
		MaxAge:         "24h",
		RunBudget:      100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.VMName != "ci-runner" || len(result.Assignments) != 1 {
		t.Fatalf("result = %+v, want one ci-runner assignment", result)
	}
	assignment := result.Assignments[0]
	wantArgs := []string{"policy", "ci-runner", "set", "idle=30m", "max-age=24h", "run-budget=100"}
	if assignment.WorkerID != "desk" || assignment.Verb != "cove" || !equalStrings(assignment.Args, wantArgs) {
		t.Fatalf("assignment = %+v, want worker desk args %+v", assignment, wantArgs)
	}
	if skipLifecyclePolicyReason(result.Skipped, "drain") != "cordoned" || skipLifecyclePolicyReason(result.Skipped, "stale") != "stale" || skipLifecyclePolicyReason(result.Skipped, "rack") != "" {
		t.Fatalf("skipped = %+v", result.Skipped)
	}

	result, err = store.PushLifecyclePolicy(LifecyclePolicyRequest{
		VMName:         "ci-runner",
		RequiredLabels: map[string]string{"zone": "desk"},
		Clear:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 0 || skipLifecyclePolicyReason(result.Skipped, "desk") != "active" {
		t.Fatalf("second lifecycle policy = %+v, want active skip for desk", result)
	}
}

func TestLifecyclePolicyArgs(t *testing.T) {
	_, args, err := lifecyclePolicyArgs(LifecyclePolicyRequest{VMName: "vm", Clear: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"policy", "vm", "clear"}; !equalStrings(args, want) {
		t.Fatalf("clear args = %+v, want %+v", args, want)
	}

	tests := []struct {
		name string
		req  LifecyclePolicyRequest
		want string
	}{
		{name: "missing vm", req: LifecyclePolicyRequest{IdleTimeout: "1m"}, want: "vm_name required"},
		{name: "missing threshold", req: LifecyclePolicyRequest{VMName: "vm"}, want: "threshold required"},
		{name: "clear with threshold", req: LifecyclePolicyRequest{VMName: "vm", Clear: true, MaxAge: "1h"}, want: "clear cannot include thresholds"},
		{name: "bad idle", req: LifecyclePolicyRequest{VMName: "vm", IdleTimeout: "bad"}, want: "idle_timeout invalid"},
		{name: "negative budget", req: LifecyclePolicyRequest{VMName: "vm", RunBudget: -1}, want: "run_budget must be non-negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := lifecyclePolicyArgs(tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("lifecyclePolicyArgs(%+v) err = %v, want %q", tt.req, err, tt.want)
			}
		})
	}
}

func TestStorePushesStorageBudgetAssignments(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "desk", Labels: map[string]string{"zone": "desk"}},
		{ID: "rack", Labels: map[string]string{"zone": "rack"}},
		{ID: "drain", Labels: map[string]string{"zone": "desk"}},
		{ID: "stale", Labels: map[string]string{"zone": "desk"}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CordonWorker("drain", "maintenance"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "desk", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "rack", Labels: map[string]string{"zone": "rack"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "drain", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	warn, hard := 75, 90
	result, err := store.PushStorageBudget(StorageBudgetRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		Target:         "500GB",
		WarnPct:        &warn,
		HardPct:        &hard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 1 {
		t.Fatalf("assignments = %+v, want 1", result.Assignments)
	}
	assignment := result.Assignments[0]
	wantArgs := []string{"storage", "budget", "set", "-target", "500GB", "-warn", "75", "-hard", "90"}
	if assignment.WorkerID != "desk" || assignment.Verb != "cove" || !equalStrings(assignment.Args, wantArgs) {
		t.Fatalf("assignment = %+v, want worker desk args %+v", assignment, wantArgs)
	}
	if skipStoragePolicyReason(result.Skipped, "drain") != "cordoned" || skipStoragePolicyReason(result.Skipped, "stale") != "stale" || skipStoragePolicyReason(result.Skipped, "rack") != "" {
		t.Fatalf("skipped = %+v", result.Skipped)
	}

	result, err = store.PushStorageBudget(StorageBudgetRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		Clear:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 0 || skipStoragePolicyReason(result.Skipped, "desk") != "active" {
		t.Fatalf("second storage budget = %+v, want active skip for desk", result)
	}
}

func TestStorePushesStoragePruneAssignments(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "desk", Labels: map[string]string{"zone": "desk"}},
		{ID: "rack", Labels: map[string]string{"zone": "rack"}},
		{ID: "drain", Labels: map[string]string{"zone": "desk"}},
		{ID: "stale", Labels: map[string]string{"zone": "desk"}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CordonWorker("drain", "maintenance"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "desk", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "rack", Labels: map[string]string{"zone": "rack"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "drain", Labels: map[string]string{"zone": "desk"}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.PushStoragePrune(StoragePruneRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		Category:       "build-scratch",
		OlderThan:      "48h",
		Apply:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 1 {
		t.Fatalf("assignments = %+v, want 1", result.Assignments)
	}
	assignment := result.Assignments[0]
	wantArgs := []string{"storage", "prune", "build-scratch", "-apply", "-older-than", "48h"}
	if assignment.WorkerID != "desk" || assignment.Verb != "cove" || !equalStrings(assignment.Args, wantArgs) {
		t.Fatalf("assignment = %+v, want worker desk args %+v", assignment, wantArgs)
	}
	if skipStoragePolicyReason(result.Skipped, "drain") != "cordoned" || skipStoragePolicyReason(result.Skipped, "stale") != "stale" || skipStoragePolicyReason(result.Skipped, "rack") != "" {
		t.Fatalf("skipped = %+v", result.Skipped)
	}

	result, err = store.PushStoragePrune(StoragePruneRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		OlderThan:      "48h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assignments) != 0 || skipStoragePolicyReason(result.Skipped, "desk") != "active" {
		t.Fatalf("second storage prune = %+v, want active skip for desk", result)
	}
}

func TestStoragePolicyArgs(t *testing.T) {
	warn, hard := 0, 0
	args, err := storageBudgetArgs(StorageBudgetRequest{Target: "1TB", WarnPct: &warn, HardPct: &hard})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"storage", "budget", "set", "-target", "1TB", "-warn", "0", "-hard", "0"}; !equalStrings(args, want) {
		t.Fatalf("budget args = %+v, want %+v", args, want)
	}
	args, err = storageBudgetArgs(StorageBudgetRequest{Clear: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"storage", "budget", "clear"}; !equalStrings(args, want) {
		t.Fatalf("budget clear args = %+v, want %+v", args, want)
	}
	args, err = storagePruneArgs(StoragePruneRequest{OlderThan: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"storage", "prune", "-older-than", "24h"}; !equalStrings(args, want) {
		t.Fatalf("prune args = %+v, want %+v", args, want)
	}

	tests := []struct {
		name string
		err  string
		fn   func() error
	}{
		{name: "budget missing target", err: "target required", fn: func() error { _, err := storageBudgetArgs(StorageBudgetRequest{}); return err }},
		{name: "budget clear with target", err: "clear cannot include thresholds", fn: func() error {
			_, err := storageBudgetArgs(StorageBudgetRequest{Clear: true, Target: "1GB"})
			return err
		}},
		{name: "budget bad warn", err: "warn_pct must be in [0,100]", fn: func() error {
			bad := 101
			_, err := storageBudgetArgs(StorageBudgetRequest{Target: "1GB", WarnPct: &bad})
			return err
		}},
		{name: "budget warn above hard", err: "warn_pct (96) must not exceed hard_pct (95)", fn: func() error {
			bad := 96
			_, err := storageBudgetArgs(StorageBudgetRequest{Target: "1GB", WarnPct: &bad})
			return err
		}},
		{name: "prune bad category", err: "unsupported", fn: func() error { _, err := storagePruneArgs(StoragePruneRequest{Category: "runs"}); return err }},
		{name: "prune bad duration", err: "older_than invalid", fn: func() error { _, err := storagePruneArgs(StoragePruneRequest{OlderThan: "bad"}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("err = %v, want %q", err, tt.err)
			}
		})
	}
}

func TestStoreEnsuresWarmPoolAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "warm", Labels: map[string]string{"zone": "desk"}, ImageRefs: []string{"macos-runner:latest"}, Capacity: Capacity{VMs: 0, MaxVMs: 1}},
		{ID: "cold", Labels: map[string]string{"zone": "desk"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}},
		{ID: "rack", Labels: map[string]string{"zone": "rack"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{
		Name:           "runner",
		ImageRef:       "macos-runner:latest",
		Size:           2,
		RequiredLabels: map[string]string{"zone": "desk"},
		Resources:      Capacity{VMs: 1},
		Args:           []string{"--net", "nat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pool.Policy != PolicyImageAffinity || result.Pool.Active != 2 || result.Pool.Slots != 2 || result.Pool.Pending != 2 {
		t.Fatalf("pool result = %+v", result.Pool)
	}
	if len(result.Created) != 2 {
		t.Fatalf("created = %+v, want 2", result.Created)
	}
	if result.Created[0].WorkerID != "warm" || result.Created[1].WorkerID != "cold" {
		t.Fatalf("created workers = %q, %q; want warm, cold", result.Created[0].WorkerID, result.Created[1].WorkerID)
	}
	for _, assignment := range result.Created {
		if assignment.WarmPool != "runner" || assignment.Verb != "cove" || assignment.ImageRef != "macos-runner:latest" {
			t.Fatalf("assignment = %+v", assignment)
		}
		wantArgs := []string{"run", "-fork-from", "macos-runner:latest", "-fork-name", warmPoolForkName("runner", assignment.ID), "-ephemeral", "-keep", "-headless", "--net", "nat"}
		if !equalStrings(assignment.Args, wantArgs) {
			t.Fatalf("args = %+v, want %+v", assignment.Args, wantArgs)
		}
	}
	result, err = store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "macos-runner:latest", Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 0 || result.Pool.Active != 2 || result.Pool.Slots != 2 {
		t.Fatalf("second ensure = %+v", result)
	}
	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pools := reopened.ListWarmPools()
	if len(pools) != 1 || pools[0].Name != "runner" || pools[0].Active != 2 || pools[0].Slots != 2 {
		t.Fatalf("reopened pools = %+v", pools)
	}
}

func TestStoreReconcileReplenishesWarmPool(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 1 {
		t.Fatalf("created = %+v, want 1", result.Created)
	}
	first := result.Created[0].ID
	leased, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if leased == nil || leased.ID != first {
		t.Fatalf("leased = %+v, want %s", leased, first)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: first, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	reconciled, err := store.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled.WarmPoolAssignments) != 1 {
		t.Fatalf("reconcile result = %+v, want one warm pool assignment", reconciled)
	}
	if reconciled.WarmPoolAssignments[0] == first {
		t.Fatalf("reconcile reused completed assignment id %q", first)
	}
	pools := store.ListWarmPools()
	if len(pools) != 1 || pools[0].Active != 1 || pools[0].Slots != 1 || pools[0].Assignments[0].ID == first {
		t.Fatalf("pools = %+v", pools)
	}
}

func TestStoreClaimsWarmPoolSlot(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 1 {
		t.Fatalf("created = %+v, want 1", result.Created)
	}
	slotID := result.Created[0].ID
	leased, err := store.AwaitAssignment("worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if leased == nil || leased.ID != slotID {
		t.Fatalf("leased = %+v, want %s", leased, slotID)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: slotID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	claim, err := store.ClaimWarmPool(WarmPoolClaimRequest{
		Name:    "runner",
		Command: []string{"/bin/echo", "ok"},
		Env:     map[string]string{"B": "2", "A": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantVMName := warmPoolForkName("runner", slotID)
	if claim.Pool != "runner" || claim.VMName != wantVMName {
		t.Fatalf("claim identity = %+v, want pool runner vm %s", claim, wantVMName)
	}
	if claim.Slot.ID != slotID || claim.Slot.Status != "claimed" {
		t.Fatalf("claimed slot = %+v, want %s claimed", claim.Slot, slotID)
	}
	wantArgs := []string{"shell", "--env", "A=1", "--env", "B=2", wantVMName, "--", "/bin/echo", "ok"}
	if claim.Assignment.WorkerID != "worker-1" || claim.Assignment.WarmPoolSlot != slotID || claim.Assignment.WarmPool != "" || claim.Assignment.Verb != "cove" || !equalStrings(claim.Assignment.Args, wantArgs) {
		t.Fatalf("claim assignment = %+v, want args %+v", claim.Assignment, wantArgs)
	}
	slot, ok := store.GetAssignment(slotID)
	if !ok {
		t.Fatal("claimed slot missing")
	}
	if slot.Status != "claimed" || slot.LastReport == nil || slot.LastReport.Status != "ready" {
		t.Fatalf("stored slot = %+v, want claimed with ready report", slot)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: slotID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	slot, _ = store.GetAssignment(slotID)
	if slot.Status != "claimed" {
		t.Fatalf("renewed slot status = %q, want claimed", slot.Status)
	}
	pools := store.ListWarmPools()
	if len(pools) != 1 || pools[0].Active != 1 || pools[0].Slots != 2 || pools[0].Pending != 1 || pools[0].Claimed != 1 {
		t.Fatalf("pools after claim = %+v, want claimed slot plus replenished replacement", pools)
	}
	if statusByAssignmentID(pools[0].Assignments, slotID) != "claimed" {
		t.Fatalf("pools after claim assignments = %+v, want claimed slot visible", pools[0].Assignments)
	}
}

func TestStoreClaimWarmPoolRequiresReadySlot(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := store.ClaimWarmPool(WarmPoolClaimRequest{Name: "runner", Command: []string{"/bin/true"}})
	if err == nil || !strings.Contains(err.Error(), "has no ready slot to claim") {
		t.Fatalf("ClaimWarmPool err = %v, want no ready slot error", err)
	}
}

func TestStoreClaimedWarmPoolSlotCountsAgainstCapacity(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 1}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	slotID := result.Created[0].ID
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: slotID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimWarmPool(WarmPoolClaimRequest{Name: "runner", Command: []string{"/bin/true"}}); err != nil {
		t.Fatal(err)
	}
	pools := store.ListWarmPools()
	if len(pools) != 1 || pools[0].Active != 0 || pools[0].Slots != 1 || pools[0].Claimed != 1 {
		t.Fatalf("pools after capacity-bound claim = %+v, want one claimed slot and no replacement", pools)
	}
}

func TestStoreDownsizesWarmPoolReadySlots(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	for _, hb := range []WorkerHeartbeat{
		{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}},
		{ID: "worker-2", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}},
	} {
		if _, err := store.UpsertHeartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 2 {
		t.Fatalf("created = %+v, want 2", result.Created)
	}
	for _, assignment := range result.Created {
		leased, err := store.AwaitAssignment(assignment.WorkerID)
		if err != nil {
			t.Fatal(err)
		}
		if leased == nil || leased.ID != assignment.ID {
			t.Fatalf("leased = %+v, want %s", leased, assignment.ID)
		}
		if _, err := store.Report(WorkerReport{ID: assignment.WorkerID, AssignmentID: assignment.ID, Status: "ready"}); err != nil {
			t.Fatal(err)
		}
	}

	now = now.Add(time.Second)
	result, err = store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 0 || len(result.Canceled) != 0 || len(result.Cleanup) != 1 {
		t.Fatalf("downsize result = %+v, want one cleanup assignment", result)
	}
	cleanup := result.Cleanup[0]
	slot, ok := store.GetAssignment(cleanup.WarmPoolSlot)
	if !ok {
		t.Fatalf("cleanup slot %q missing", cleanup.WarmPoolSlot)
	}
	if slot.Status != "draining" {
		t.Fatalf("slot status = %q, want draining", slot.Status)
	}
	wantArgs := warmPoolStopArgs(WarmPoolAssignmentVMName(slot))
	if cleanup.WorkerID != slot.WorkerID || cleanup.Verb != "cove" || !equalStrings(cleanup.Args, wantArgs) {
		t.Fatalf("cleanup = %+v, want worker %q args %+v", cleanup, slot.WorkerID, wantArgs)
	}
	pools := store.ListWarmPools()
	if len(pools) != 1 || pools[0].Active != 1 || pools[0].Slots != 2 || pools[0].Ready != 1 || pools[0].Draining != 1 {
		t.Fatalf("pools after downsize = %+v, want one ready slot and one draining slot", pools)
	}
	if _, err := store.Report(WorkerReport{ID: slot.WorkerID, AssignmentID: slot.ID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	slot, _ = store.GetAssignment(slot.ID)
	if slot.Status != "draining" {
		t.Fatalf("slot status after ready report = %q, want draining", slot.Status)
	}
	if _, err := store.Report(WorkerReport{ID: slot.WorkerID, AssignmentID: slot.ID, Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	slot, _ = store.GetAssignment(slot.ID)
	if slot.Status != "complete" {
		t.Fatalf("slot status after complete report = %q, want complete", slot.Status)
	}
}

func TestStoreDownsizesWarmPoolPendingSlots(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) != 2 {
		t.Fatalf("created = %+v, want 2", result.Created)
	}
	result, err = store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Canceled) != 2 || len(result.Cleanup) != 0 || result.Pool.Active != 0 || result.Pool.Slots != 0 || result.Pool.Terminal != 2 {
		t.Fatalf("downsize result = %+v, want two canceled pending slots", result)
	}
	for _, id := range result.Canceled {
		assignment, ok := store.GetAssignment(id)
		if !ok {
			t.Fatalf("assignment %q missing", id)
		}
		if assignment.Status != "canceled" {
			t.Fatalf("assignment %s status = %q, want canceled", id, assignment.Status)
		}
	}
}

func TestStoreDeletesWarmPoolReadySlots(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	slotID := result.Created[0].ID
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: slotID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	deleted, err := store.DeleteWarmPool("runner")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Pool != "runner" || len(deleted.Canceled) != 0 || len(deleted.Cleanup) != 1 || len(deleted.Deferred) != 0 {
		t.Fatalf("delete result = %+v, want one cleanup", deleted)
	}
	cleanup := deleted.Cleanup[0]
	slot, ok := store.GetAssignment(slotID)
	if !ok {
		t.Fatal("slot missing")
	}
	if slot.Status != "draining" {
		t.Fatalf("slot status = %q, want draining", slot.Status)
	}
	if cleanup.WorkerID != "worker-1" || cleanup.WarmPoolSlot != slotID || !equalStrings(cleanup.Args, warmPoolStopArgs(WarmPoolAssignmentVMName(slot))) {
		t.Fatalf("cleanup = %+v", cleanup)
	}
	if _, ok := store.GetWarmPool("runner"); ok {
		t.Fatal("deleted warm pool still present")
	}
	if pools := store.ListWarmPools(); len(pools) != 0 {
		t.Fatalf("pools = %+v, want none", pools)
	}
}

func TestStoreDeletesWarmPoolWithClaimedSlot(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 1}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.EnsureWarmPool(WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	slotID := result.Created[0].ID
	if _, err := store.AwaitAssignment("worker-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Report(WorkerReport{ID: "worker-1", AssignmentID: slotID, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimWarmPool(WarmPoolClaimRequest{Name: "runner", Command: []string{"/bin/true"}}); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.DeleteWarmPool("runner")
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Cleanup) != 0 || len(deleted.Canceled) != 0 || len(deleted.Deferred) != 1 || deleted.Deferred[0] != slotID {
		t.Fatalf("delete result = %+v, want deferred claimed slot %s", deleted, slotID)
	}
	slot, _ := store.GetAssignment(slotID)
	if slot.Status != "claimed" {
		t.Fatalf("slot status = %q, want claimed", slot.Status)
	}
	if _, ok := store.GetWarmPool("runner"); ok {
		t.Fatal("deleted warm pool still present")
	}
}

func TestStoreWarmPoolRejectsReservedArgs(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	_, err := store.EnsureWarmPool(WarmPoolRequest{
		Name:     "runner",
		ImageRef: "base:v1",
		Size:     1,
		Args:     []string{"-fork-name", "custom"},
	})
	if err == nil || !strings.Contains(err.Error(), "warm pool args must not set -fork-name") {
		t.Fatalf("EnsureWarmPool err = %v, want reserved arg error", err)
	}
}

func TestStoreScheduleRequiresReadyWorker(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "stale"}); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := store.CreateAssignment(Assignment{Policy: PolicyLeastLoaded, Verb: "cove"}); err == nil {
		t.Fatal("CreateAssignment error = nil, want no ready worker")
	}
}

func TestHandlerWorkerProtocol(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{
		ID:      "mini-1",
		Host:    "mini.local",
		Version: "test-version",
		Labels:  map[string]string{"zone": "desk"},
		Capacity: Capacity{
			CPUs:        12,
			MemoryBytes: 64 << 30,
			VMs:         2,
			Images:      5,
		},
	}, &record)
	if record.ID != "mini-1" || record.Capacity.CPUs != 12 {
		t.Fatalf("register response = %+v", record)
	}
	if got := record.ImageRefs; len(got) != 0 {
		t.Fatalf("image refs = %+v, want none", got)
	}

	resp, err := http.Get(server.URL + "/v1/workers/mini-1/assignments")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("assignments status = %d, want 204", resp.StatusCode)
	}

	postJSON(t, server.URL+"/v1/workers/mini-1/reports", WorkerReport{
		AssignmentID: "assignment-1",
		Status:       "complete",
	}, &record)
	if record.Report == nil || record.Report.Status != "complete" {
		t.Fatalf("report response = %+v", record)
	}

	var list struct {
		Workers []HostRecord `json:"workers"`
	}
	getJSON(t, server.URL+"/v1/workers", &list)
	if len(list.Workers) != 1 || list.Workers[0].ID != "mini-1" {
		t.Fatalf("list response = %+v", list)
	}
}

func TestHandlerAssignmentProtocol(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "mini-1"}, &record)
	var created Assignment
	postJSON(t, server.URL+"/v1/assignments", Assignment{
		ID:       "assignment-1",
		WorkerID: "mini-1",
		Verb:     "noop",
	}, &created)
	if created.Status != "pending" {
		t.Fatalf("created assignment = %+v", created)
	}

	var leased Assignment
	getJSON(t, server.URL+"/v1/workers/mini-1/assignments", &leased)
	if leased.ID != "assignment-1" || leased.Status != "leased" || leased.LeasedTo != "mini-1" {
		t.Fatalf("leased assignment = %+v", leased)
	}

	postJSON(t, server.URL+"/v1/workers/mini-1/reports", WorkerReport{
		AssignmentID: "assignment-1",
		Status:       "complete",
	}, &record)

	var finished Assignment
	getJSON(t, server.URL+"/v1/assignments/assignment-1", &finished)
	if finished.Status != "complete" || finished.LastReport == nil || finished.LastReport.Status != "complete" {
		t.Fatalf("finished assignment = %+v", finished)
	}

	var list struct {
		Assignments []Assignment `json:"assignments"`
	}
	getJSON(t, server.URL+"/v1/assignments", &list)
	if len(list.Assignments) != 1 || list.Assignments[0].ID != "assignment-1" {
		t.Fatalf("list = %+v", list)
	}
}

func TestHandlerSchedulesAssignment(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "warm", ImageRefs: []string{"macos-runner:latest"}, Capacity: Capacity{VMs: 5}}, &record)
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "cold", Capacity: Capacity{VMs: 1}}, &record)

	var created Assignment
	postJSON(t, server.URL+"/v1/assignments", Assignment{
		ID:       "assignment-1",
		Policy:   PolicyImageAffinity,
		ImageRef: "macos-runner:latest",
		Verb:     "cove",
	}, &created)
	if created.WorkerID != "warm" {
		t.Fatalf("created assignment = %+v, want warm worker", created)
	}
}

func TestHandlerPlansPlacement(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "warm", ImageRefs: []string{"macos-runner:latest"}, Capacity: Capacity{VMs: 1, MaxVMs: 3}}, &record)
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "cold", Capacity: Capacity{VMs: 0, MaxVMs: 3}}, &record)

	var plan PlacementPlan
	postJSON(t, server.URL+"/v1/placements/plan", PlacementPlanRequest{
		Assignment: Assignment{
			Policy:    PolicyImageAffinity,
			ImageRef:  "macos-runner:latest",
			Resources: Capacity{VMs: 2},
			Verb:      "cove",
		},
		Limit: 1,
	}, &plan)
	if plan.Policy != PolicyImageAffinity || len(plan.Candidates) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if got := plan.Candidates[0]; got.WorkerID != "warm" || got.Rank != 1 || got.RequestedVMs != 2 || !got.HasImage {
		t.Fatalf("candidate = %+v, want warm image candidate", got)
	}
}

func TestHandlerWorkerCordonLifecycle(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "drain"}, &record)
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "ready"}, &record)
	postJSON(t, server.URL+"/v1/workers/drain/cordon", WorkerLifecycle{Reason: "maintenance"}, &record)
	if !record.Cordoned || record.Status != "cordoned" || record.CordonReason != "maintenance" {
		t.Fatalf("cordon response = %+v", record)
	}

	var created Assignment
	postJSON(t, server.URL+"/v1/assignments", Assignment{
		ID:     "assignment-1",
		Policy: PolicyLeastLoaded,
		Verb:   "noop",
	}, &created)
	if created.WorkerID != "ready" {
		t.Fatalf("created assignment = %+v, want ready worker", created)
	}

	postJSON(t, server.URL+"/v1/workers/drain/uncordon", map[string]string{}, &record)
	if record.Cordoned || record.Status != "ready" {
		t.Fatalf("uncordon response = %+v", record)
	}
}

func TestHandlerWorkerDrainStopsHostedSandboxes(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{MaxVMs: 4}}, &record)
	var sandbox SandboxStatus
	postJSON(t, server.URL+"/v1/sandboxes", SandboxRequest{ID: "job-1", ImageRef: "base:v1"}, &sandbox)
	var leased Assignment
	getJSON(t, server.URL+"/v1/workers/worker-1/assignments", &leased)
	postJSON(t, server.URL+"/v1/workers/worker-1/reports", WorkerReport{AssignmentID: leased.ID, Status: "ready"}, &record)

	var drain WorkerDrainResult
	postJSON(t, server.URL+"/v1/workers/worker-1/drain", WorkerLifecycle{Reason: "maintenance"}, &drain)
	if !drain.Worker.Cordoned || drain.Worker.CordonReason != "maintenance" {
		t.Fatalf("drain worker = %+v, want cordoned maintenance", drain.Worker)
	}
	if len(drain.Sandboxes) != 1 || drain.Sandboxes[0].ID != "job-1" || drain.Sandboxes[0].Cleanup == nil {
		t.Fatalf("drain result = %+v, want job-1 cleanup", drain)
	}
	getJSON(t, server.URL+"/v1/sandboxes/job-1", &sandbox)
	if sandbox.Status != "draining" {
		t.Fatalf("sandbox after drain = %+v, want draining", sandbox)
	}
}

func TestHandlerOperationsSummary(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-a", Namespace: "team-a", Role: ServiceAccountRoleViewer, Token: "token-a"}, &account)
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{MaxVMs: 2}}, &record)
	var sandbox SandboxStatus
	postJSON(t, server.URL+"/v1/sandboxes", SandboxRequest{Namespace: "team-a", ID: "job-1", ImageRef: "base:v1"}, &sandbox)
	var assignment Assignment
	postJSON(t, server.URL+"/v1/assignments", Assignment{Namespace: "team-b", WorkerID: "worker-1", Verb: "noop"}, &assignment)

	var summary OperationsSummary
	getJSON(t, server.URL+"/v1/operations/summary?namespace=team-a", &summary)
	if summary.Namespace != "team-a" || summary.Workers.Total != 1 || summary.Assignments.Total != 1 || summary.Sandboxes.Total != 1 {
		t.Fatalf("operations summary = %+v, want team-a counts only with global workers", summary)
	}
	if code := getJSONStatus(t, server.URL+"/v1/operations/summary", "token-a"); code != http.StatusForbidden {
		t.Fatalf("scoped operations summary status = %d, want %d", code, http.StatusForbidden)
	}
}

func TestHandlerPrepareImage(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1"}, &record)
	var result ImagePrepareResult
	postJSON(t, server.URL+"/v1/images/prepare", ImagePrepareRequest{
		SourceRef: "registry.example/cove/macos-runner:latest",
		ImageRef:  "macos-runner:latest",
	}, &result)
	if len(result.Assignments) != 1 || result.Assignments[0].WorkerID != "worker-1" {
		t.Fatalf("prepare result = %+v", result)
	}
}

func TestHandlerImageGC(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", Labels: map[string]string{"zone": "desk"}}, &record)
	var result ImageGCResult
	postJSON(t, server.URL+"/v1/images/gc", ImageGCRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		OlderThan:      "1h",
		Apply:          true,
	}, &result)
	if len(result.Assignments) != 1 || result.Assignments[0].WorkerID != "worker-1" {
		t.Fatalf("image gc result = %+v", result)
	}
	wantArgs := []string{"image", "gc", "-yes", "-older-than", "1h"}
	if !equalStrings(result.Assignments[0].Args, wantArgs) {
		t.Fatalf("args = %+v, want %+v", result.Assignments[0].Args, wantArgs)
	}
}

func TestHandlerLifecyclePolicy(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", Labels: map[string]string{"zone": "desk"}}, &record)
	var result LifecyclePolicyResult
	postJSON(t, server.URL+"/v1/policies/lifecycle", LifecyclePolicyRequest{
		VMName:         "ci-runner",
		RequiredLabels: map[string]string{"zone": "desk"},
		IdleTimeout:    "15m",
		RunBudget:      5,
	}, &result)
	if result.VMName != "ci-runner" || len(result.Assignments) != 1 || result.Assignments[0].WorkerID != "worker-1" {
		t.Fatalf("lifecycle policy result = %+v", result)
	}
	wantArgs := []string{"policy", "ci-runner", "set", "idle=15m", "run-budget=5"}
	if !equalStrings(result.Assignments[0].Args, wantArgs) {
		t.Fatalf("args = %+v, want %+v", result.Assignments[0].Args, wantArgs)
	}
}

func TestHandlerStorageBudget(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", Labels: map[string]string{"zone": "desk"}}, &record)
	warn, hard := 70, 90
	var result StorageBudgetResult
	postJSON(t, server.URL+"/v1/storage/budget", StorageBudgetRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		Target:         "750GB",
		WarnPct:        &warn,
		HardPct:        &hard,
	}, &result)
	if len(result.Assignments) != 1 || result.Assignments[0].WorkerID != "worker-1" {
		t.Fatalf("storage budget result = %+v", result)
	}
	wantArgs := []string{"storage", "budget", "set", "-target", "750GB", "-warn", "70", "-hard", "90"}
	if !equalStrings(result.Assignments[0].Args, wantArgs) {
		t.Fatalf("args = %+v, want %+v", result.Assignments[0].Args, wantArgs)
	}
}

func TestHandlerStoragePrune(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", Labels: map[string]string{"zone": "desk"}}, &record)
	var result StoragePruneResult
	postJSON(t, server.URL+"/v1/storage/prune", StoragePruneRequest{
		RequiredLabels: map[string]string{"zone": "desk"},
		OlderThan:      "168h",
	}, &result)
	if len(result.Assignments) != 1 || result.Assignments[0].WorkerID != "worker-1" {
		t.Fatalf("storage prune result = %+v", result)
	}
	wantArgs := []string{"storage", "prune", "-older-than", "168h"}
	if !equalStrings(result.Assignments[0].Args, wantArgs) {
		t.Fatalf("args = %+v, want %+v", result.Assignments[0].Args, wantArgs)
	}
}

func TestHandlerWarmPools(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 2}}, &record)
	var result WarmPoolResult
	postJSON(t, server.URL+"/v1/warm-pools", WarmPoolRequest{
		Name:     "runner",
		ImageRef: "base:v1",
		Size:     1,
	}, &result)
	if result.Pool.Name != "runner" || result.Pool.Active != 1 || result.Pool.Slots != 1 || result.Pool.Pending != 1 || len(result.Created) != 1 {
		t.Fatalf("warm pool result = %+v", result)
	}
	if result.Created[0].WorkerID != "worker-1" || result.Created[0].WarmPool != "runner" {
		t.Fatalf("created assignment = %+v", result.Created[0])
	}

	var list struct {
		WarmPools []WarmPoolStatus `json:"warm_pools"`
	}
	getJSON(t, server.URL+"/v1/warm-pools", &list)
	if len(list.WarmPools) != 1 || list.WarmPools[0].Name != "runner" || list.WarmPools[0].Active != 1 || list.WarmPools[0].Slots != 1 {
		t.Fatalf("warm pool list = %+v", list)
	}

	var leased Assignment
	getJSON(t, server.URL+"/v1/workers/worker-1/assignments", &leased)
	if leased.ID != result.Created[0].ID {
		t.Fatalf("leased warm slot = %+v, want %s", leased, result.Created[0].ID)
	}
	postJSON(t, server.URL+"/v1/workers/worker-1/reports", WorkerReport{
		AssignmentID: leased.ID,
		Status:       "ready",
	}, &record)

	var claim WarmPoolClaimResult
	postJSON(t, server.URL+"/v1/warm-pools/claim", WarmPoolClaimRequest{
		Name:    "runner",
		Command: []string{"/bin/true"},
	}, &claim)
	if claim.Pool != "runner" || claim.Slot.ID != leased.ID || claim.Slot.Status != "claimed" {
		t.Fatalf("claim response = %+v", claim)
	}
	wantArgs := []string{"shell", claim.VMName, "--", "/bin/true"}
	if claim.Assignment.WorkerID != "worker-1" || claim.Assignment.WarmPoolSlot != leased.ID || !equalStrings(claim.Assignment.Args, wantArgs) {
		t.Fatalf("claim assignment = %+v, want args %+v", claim.Assignment, wantArgs)
	}
}

func TestHandlerWarmPoolDownsize(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}, &record)
	var result WarmPoolResult
	postJSON(t, server.URL+"/v1/warm-pools", WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 2}, &result)
	if len(result.Created) != 2 {
		t.Fatalf("warm pool create = %+v, want 2 created", result)
	}
	postJSON(t, server.URL+"/v1/warm-pools", WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 0}, &result)
	if len(result.Canceled) != 2 || len(result.Cleanup) != 0 || result.Pool.Active != 0 || result.Pool.Terminal != 2 {
		t.Fatalf("warm pool downsize = %+v, want two canceled pending slots", result)
	}
}

func TestHandlerWarmPoolGetDelete(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}, &record)
	var result WarmPoolResult
	postJSON(t, server.URL+"/v1/warm-pools", WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 2}, &result)
	var status WarmPoolStatus
	getJSON(t, server.URL+"/v1/warm-pools/runner", &status)
	if status.Name != "runner" || status.Active != 2 || status.Slots != 2 || status.Pending != 2 {
		t.Fatalf("warm pool status = %+v, want runner active 2", status)
	}
	var deleted WarmPoolDeleteResult
	deleteJSON(t, server.URL+"/v1/warm-pools/runner", &deleted)
	if deleted.Pool != "runner" || len(deleted.Canceled) != 2 || len(deleted.Cleanup) != 0 {
		t.Fatalf("warm pool delete = %+v, want two canceled pending slots", deleted)
	}
	resp, err := http.Get(server.URL + "/v1/warm-pools/runner")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted warm pool status = %d, want 404", resp.StatusCode)
	}
}

func TestHandlerAudit(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1"}, &record)
	var created Assignment
	postJSON(t, server.URL+"/v1/assignments", Assignment{WorkerID: "worker-1", Verb: "noop"}, &created)

	var list struct {
		Events []AuditEvent `json:"events"`
	}
	getJSON(t, server.URL+"/v1/audit?limit=1", &list)
	if len(list.Events) != 1 || list.Events[0].Action != "assignment.create" || list.Events[0].AssignmentID != created.ID {
		t.Fatalf("audit events = %+v, want latest assignment.create %s", list.Events, created.ID)
	}
	var verify AuditVerifyResult
	getJSON(t, server.URL+"/v1/audit/verify", &verify)
	if !verify.OK || verify.Events == 0 || verify.HeadHash == "" {
		t.Fatalf("audit verify = %+v, want ok with head hash", verify)
	}
	resp, err := http.Get(server.URL + "/v1/audit?limit=bad")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad audit limit status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerSandboxes(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}, &record)
	var created SandboxStatus
	postJSON(t, server.URL+"/v1/sandboxes", SandboxRequest{ID: "job-1", ImageRef: "base:v1"}, &created)
	if created.ID != "job-1" || created.WorkerID != "worker-1" || created.Assignment.SandboxRole != "run" {
		t.Fatalf("created sandbox = %+v", created)
	}
	var list struct {
		Sandboxes []SandboxStatus `json:"sandboxes"`
	}
	getJSON(t, server.URL+"/v1/sandboxes", &list)
	if len(list.Sandboxes) != 1 || list.Sandboxes[0].ID != "job-1" {
		t.Fatalf("sandboxes = %+v, want job-1", list.Sandboxes)
	}
	var got SandboxStatus
	getJSON(t, server.URL+"/v1/sandboxes/job-1", &got)
	if got.ID != "job-1" || got.VMName != "cove-sandbox-job-1" {
		t.Fatalf("sandbox = %+v, want job-1", got)
	}
	var lease SandboxLeaseResult
	postJSON(t, server.URL+"/v1/sandboxes/job-1/lease", SandboxLeaseRequest{Holder: "client-a", TTL: "1s"}, &lease)
	if lease.Sandbox.ID != "job-1" || lease.Lease.Holder != "client-a" || lease.Sandbox.Lease == nil {
		t.Fatalf("sandbox lease = %+v, want client-a lease on job-1", lease)
	}
	getJSON(t, server.URL+"/v1/sandboxes/job-1", &got)
	if got.Lease == nil || got.Lease.Holder != "client-a" {
		t.Fatalf("sandbox after lease = %+v, want client-a lease", got)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes/job-1/lease", "", SandboxLeaseRequest{Holder: "client-b"}); code != http.StatusConflict {
		t.Fatalf("conflicting sandbox lease status = %d, want 409", code)
	}
	var released SandboxStatus
	deleteJSON(t, server.URL+"/v1/sandboxes/job-1/lease?holder=client-a", &released)
	if released.Lease != nil {
		t.Fatalf("sandbox release = %+v, want no lease", released)
	}
	var deleted SandboxDeleteResult
	deleteJSON(t, server.URL+"/v1/sandboxes/job-1", &deleted)
	if !deleted.Canceled || deleted.ID != "job-1" {
		t.Fatalf("deleted sandbox = %+v, want canceled job-1", deleted)
	}
	postJSON(t, server.URL+"/v1/sandboxes", SandboxRequest{ID: "job-2", ImageRef: "base:v1"}, &created)
	var stopped SandboxStopResult
	postJSON(t, server.URL+"/v1/sandboxes/job-2/stop", map[string]string{}, &stopped)
	if !stopped.Canceled || stopped.ID != "job-2" {
		t.Fatalf("stopped sandbox = %+v, want canceled job-2", stopped)
	}
	var wait SandboxWaitResult
	postJSON(t, server.URL+"/v1/sandboxes/job-2/wait?timeout=0", map[string]string{}, &wait)
	if !wait.Done || wait.Sandbox.ID != "job-2" || wait.Sandbox.Status != "canceled" {
		t.Fatalf("wait sandbox = %+v, want canceled job-2", wait)
	}
	var started SandboxStartResult
	postJSON(t, server.URL+"/v1/sandboxes/job-2/start", map[string]string{}, &started)
	if !started.Started || started.ID != "job-2" || started.Status != "pending" {
		t.Fatalf("start sandbox = %+v, want pending job-2", started)
	}
	var restart SandboxRestartResult
	postJSON(t, server.URL+"/v1/sandboxes/job-2/restart", map[string]string{}, &restart)
	if restart.Restarting || restart.ID != "job-2" || restart.Status != "pending" {
		t.Fatalf("restart pending sandbox = %+v, want pending no-op", restart)
	}
}

func TestHandlerSandboxListFilters(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1", "base:v2"}, Capacity: Capacity{MaxVMs: 4}}, &record)
	var ready SandboxStatus
	postJSON(t, server.URL+"/v1/sandboxes", SandboxRequest{ID: "job-ready", ImageRef: "base:v1"}, &ready)
	var leased Assignment
	getJSON(t, server.URL+"/v1/workers/worker-1/assignments", &leased)
	postJSON(t, server.URL+"/v1/workers/worker-1/reports", WorkerReport{AssignmentID: leased.ID, Status: "ready"}, &record)
	var pending SandboxStatus
	postJSON(t, server.URL+"/v1/sandboxes", SandboxRequest{ID: "job-pending", ImageRef: "base:v2"}, &pending)

	var list struct {
		Sandboxes []SandboxStatus `json:"sandboxes"`
	}
	getJSON(t, server.URL+"/v1/sandboxes?status=ready", &list)
	if len(list.Sandboxes) != 1 || list.Sandboxes[0].ID != "job-ready" {
		t.Fatalf("ready sandboxes = %+v, want job-ready", list.Sandboxes)
	}
	getJSON(t, server.URL+"/v1/sandboxes?image_ref=base:v2", &list)
	if len(list.Sandboxes) != 1 || list.Sandboxes[0].ID != "job-pending" {
		t.Fatalf("base:v2 sandboxes = %+v, want job-pending", list.Sandboxes)
	}
	getJSON(t, server.URL+"/v1/sandboxes?worker_id=worker-1&limit=1", &list)
	if len(list.Sandboxes) != 1 || list.Sandboxes[0].ID != "job-ready" {
		t.Fatalf("limited worker sandboxes = %+v, want first sandbox", list.Sandboxes)
	}
	if code := getJSONStatus(t, server.URL+"/v1/sandboxes?limit=-1", ""); code != http.StatusBadRequest {
		t.Fatalf("bad sandbox list limit status = %d, want 400", code)
	}
}

func TestHandlerSandboxMetering(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-a", Namespace: "team-a", Token: "token-a"}, &account)
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-b", Namespace: "team-b", Token: "token-b"}, &account)
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}, &record)
	var created SandboxStatus
	postJSONAuth(t, server.URL+"/v1/sandboxes", "token-a", SandboxRequest{ID: "job-1", ImageRef: "base:v1", Resources: Capacity{CPUs: 2, MemoryBytes: 1024}}, &created)
	var leased Assignment
	getJSON(t, server.URL+"/v1/workers/worker-1/assignments", &leased)
	now = now.Add(time.Second)
	postJSON(t, server.URL+"/v1/workers/worker-1/reports", WorkerReport{AssignmentID: leased.ID, Status: "running"}, &record)
	now = now.Add(2 * time.Second)
	postJSON(t, server.URL+"/v1/workers/worker-1/reports", WorkerReport{AssignmentID: leased.ID, Status: "ready"}, &record)
	now = now.Add(time.Second)
	var stopped SandboxStopResult
	postJSON(t, server.URL+"/v1/sandboxes/job-1/stop", map[string]string{}, &stopped)

	var metering SandboxMeteringResult
	getJSON(t, server.URL+"/v1/metering/sandboxes?sandbox_id=job-1", &metering)
	if len(metering.Records) != 2 || metering.Summary.DurationMillis != 3000 || metering.Summary.CPUMillis != 6000 {
		t.Fatalf("metering = %+v, want 2 records and 3s/6cpu-s", metering)
	}
	getJSON(t, server.URL+"/v1/sandboxes/job-1/metering", &metering)
	if metering.Summary.SandboxID != "job-1" || metering.Summary.DurationMillis != 3000 {
		t.Fatalf("sandbox metering = %+v, want job-1 3s", metering)
	}
	getJSONAuth(t, server.URL+"/v1/metering/sandboxes?sandbox_id=job-1", "token-a", &metering)
	if len(metering.Records) != 2 || metering.Summary.Namespace != "team-a" {
		t.Fatalf("team-a metering = %+v, want scoped records", metering)
	}
	getJSONAuth(t, server.URL+"/v1/metering/sandboxes?sandbox_id=job-1", "token-b", &metering)
	if len(metering.Records) != 0 || metering.Summary.Records != 0 {
		t.Fatalf("team-b metering = %+v, want none", metering)
	}
	if code := getJSONStatus(t, server.URL+"/v1/sandboxes/job-1/metering", "token-b"); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox metering status = %d, want 404", code)
	}
}

func TestHandlerSandboxExec(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-a", Namespace: "team-a", Token: "token-a"}, &account)
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-b", Namespace: "team-b", Token: "token-b"}, &account)
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}, &record)
	var created SandboxStatus
	postJSONAuth(t, server.URL+"/v1/sandboxes", "token-a", SandboxRequest{ID: "job-1", ImageRef: "base:v1"}, &created)
	var leased Assignment
	getJSON(t, server.URL+"/v1/workers/worker-1/assignments", &leased)
	now = now.Add(time.Second)
	postJSON(t, server.URL+"/v1/workers/worker-1/reports", WorkerReport{AssignmentID: leased.ID, Status: "ready"}, &record)

	var execResult SandboxExecResult
	postJSONAuth(t, server.URL+"/v1/sandboxes/job-1/exec?timeout=0", "token-a", SandboxExecRequest{
		Command: []string{"/bin/echo", "ok"},
		Env:     map[string]string{"A": "1"},
	}, &execResult)
	if execResult.Done || execResult.Assignment.SandboxRole != sandboxRoleExec || execResult.Assignment.WorkerID != "worker-1" {
		t.Fatalf("exec result = %+v, want pending same-worker exec", execResult)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes/job-1/exec?timeout=0", "token-b", SandboxExecRequest{Command: []string{"true"}}); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox exec status = %d, want 404", code)
	}
	var controlResult SandboxControlResult
	postJSONAuth(t, server.URL+"/v1/sandboxes/job-1/control?timeout=0", "token-a", SandboxControlRequest{
		Type: "key",
		Key:  map[string]any{"key_code": float64(36), "key_down": true, "modifiers": float64(1 << 20), "use_cg_event": true},
	}, &controlResult)
	if controlResult.Done || controlResult.Type != "key" || controlResult.Assignment.SandboxRole != sandboxRoleControl || controlResult.Assignment.WorkerID != "worker-1" {
		t.Fatalf("control result = %+v, want pending same-worker control", controlResult)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes/job-1/control?timeout=0", "token-b", SandboxControlRequest{Type: "text", Text: map[string]any{"text": "hi"}}); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox control status = %d, want 404", code)
	}
}

func TestHandlerSandboxNamespaceScope(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-a", Namespace: "team-a", Token: "token-a"}, &account)
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-b", Namespace: "team-b", Token: "token-b"}, &account)
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{VMs: 0, MaxVMs: 4}}, &record)

	var created SandboxStatus
	postJSONAuth(t, server.URL+"/v1/sandboxes", "token-a", SandboxRequest{ID: "job-1", ImageRef: "base:v1"}, &created)
	if created.Namespace != "team-a" {
		t.Fatalf("sandbox namespace = %q, want team-a", created.Namespace)
	}
	if code := getJSONStatus(t, server.URL+"/v1/sandboxes/job-1", "token-b"); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox GET status = %d, want 404", code)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes/job-1/stop", "token-b", map[string]string{}); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox stop status = %d, want 404", code)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes/job-1/start", "token-b", map[string]string{}); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox start status = %d, want 404", code)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes/job-1/restart", "token-b", map[string]string{}); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox restart status = %d, want 404", code)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes/job-1/lease", "token-b", SandboxLeaseRequest{Holder: "client-b"}); code != http.StatusNotFound {
		t.Fatalf("cross-namespace sandbox lease status = %d, want 404", code)
	}
	var lease SandboxLeaseResult
	postJSONAuth(t, server.URL+"/v1/sandboxes/job-1/lease", "token-a", SandboxLeaseRequest{Holder: "client-a"}, &lease)
	if lease.Sandbox.Namespace != "team-a" || lease.Lease.Holder != "client-a" {
		t.Fatalf("team-a sandbox lease = %+v, want team-a client-a", lease)
	}
	var list struct {
		Sandboxes []SandboxStatus `json:"sandboxes"`
	}
	getJSONAuth(t, server.URL+"/v1/sandboxes", "token-a", &list)
	if len(list.Sandboxes) != 1 || list.Sandboxes[0].ID != "job-1" {
		t.Fatalf("team-a sandboxes = %+v, want job-1", list.Sandboxes)
	}
	getJSONAuth(t, server.URL+"/v1/sandboxes", "token-b", &list)
	if len(list.Sandboxes) != 0 {
		t.Fatalf("team-b sandboxes = %+v, want none", list.Sandboxes)
	}
}

func TestHandlerServiceAccountActor(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "ci", Token: "secret-token"}, &account)
	if account.ServiceAccount.Name != "ci" {
		t.Fatalf("account = %+v, want ci", account)
	}
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1"}, &record)
	var created Assignment
	postJSONAuth(t, server.URL+"/v1/assignments", "secret-token", Assignment{WorkerID: "worker-1", Verb: "noop"}, &created)

	var list struct {
		Events []AuditEvent `json:"events"`
	}
	getJSON(t, server.URL+"/v1/audit?limit=1", &list)
	if len(list.Events) != 1 || list.Events[0].Actor != "service-account:ci" || list.Events[0].AssignmentID != created.ID {
		t.Fatalf("audit events = %+v, want service-account:ci assignment %s", list.Events, created.ID)
	}

	var accounts struct {
		ServiceAccounts []ServiceAccount `json:"service_accounts"`
	}
	getJSON(t, server.URL+"/v1/service-accounts", &accounts)
	if len(accounts.ServiceAccounts) != 1 || accounts.ServiceAccounts[0].Name != "ci" {
		t.Fatalf("service accounts = %+v, want ci", accounts.ServiceAccounts)
	}
}

func TestHandlerServiceAccountNamespaceScope(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-a", Namespace: "team-a", Token: "token-a"}, &account)
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-b", Namespace: "team-b", Token: "token-b"}, &account)
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1"}, &record)
	if code := getJSONStatus(t, server.URL+"/v1/assignments", "bad-token"); code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer status = %d, want 401", code)
	}

	var teamA Assignment
	postJSONAuth(t, server.URL+"/v1/assignments", "token-a", Assignment{WorkerID: "worker-1", Verb: "noop"}, &teamA)
	if teamA.Namespace != "team-a" {
		t.Fatalf("team-a namespace = %q", teamA.Namespace)
	}
	var teamB Assignment
	postJSONAuth(t, server.URL+"/v1/assignments", "token-b", Assignment{Namespace: "team-b", WorkerID: "worker-1", Verb: "noop"}, &teamB)
	if teamB.Namespace != "team-b" {
		t.Fatalf("team-b namespace = %q", teamB.Namespace)
	}
	if code := postJSONStatus(t, server.URL+"/v1/assignments", "token-a", Assignment{Namespace: "team-b", WorkerID: "worker-1", Verb: "noop"}); code != http.StatusForbidden {
		t.Fatalf("cross-namespace POST status = %d, want 403", code)
	}

	var assignments struct {
		Assignments []Assignment `json:"assignments"`
	}
	getJSONAuth(t, server.URL+"/v1/assignments", "token-a", &assignments)
	if len(assignments.Assignments) != 1 || assignments.Assignments[0].ID != teamA.ID {
		t.Fatalf("team-a assignments = %+v, want %s only", assignments.Assignments, teamA.ID)
	}
	getJSON(t, server.URL+"/v1/assignments?namespace=team-b", &assignments)
	if len(assignments.Assignments) != 1 || assignments.Assignments[0].ID != teamB.ID {
		t.Fatalf("team-b assignments = %+v, want %s only", assignments.Assignments, teamB.ID)
	}
	if code := getJSONStatus(t, server.URL+"/v1/assignments/"+teamB.ID, "token-a"); code != http.StatusNotFound {
		t.Fatalf("cross-namespace GET status = %d, want 404", code)
	}

	var accounts struct {
		ServiceAccounts []ServiceAccount `json:"service_accounts"`
	}
	getJSONAuth(t, server.URL+"/v1/service-accounts", "token-a", &accounts)
	if len(accounts.ServiceAccounts) != 1 || accounts.ServiceAccounts[0].Name != "team-a" || accounts.ServiceAccounts[0].Namespace != "team-a" {
		t.Fatalf("team-a service accounts = %+v", accounts.ServiceAccounts)
	}
	var audit struct {
		Events []AuditEvent `json:"events"`
	}
	getJSONAuth(t, server.URL+"/v1/audit", "token-a", &audit)
	if len(audit.Events) == 0 {
		t.Fatal("team-a audit is empty")
	}
	for _, event := range audit.Events {
		if event.Namespace != "team-a" {
			t.Fatalf("audit event namespace = %q, want team-a: %+v", event.Namespace, event)
		}
	}
	if code := getJSONStatus(t, server.URL+"/v1/audit/verify", "token-a"); code != http.StatusForbidden {
		t.Fatalf("scoped audit verify status = %d, want 403", code)
	}
}

func TestHandlerServiceAccountRoles(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "viewer", Namespace: "team-a", Role: ServiceAccountRoleViewer, Token: "viewer-token"}, &account)
	if account.ServiceAccount.Role != ServiceAccountRoleViewer {
		t.Fatalf("viewer role = %q", account.ServiceAccount.Role)
	}
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "operator", Namespace: "team-a", Role: ServiceAccountRoleOperator, Token: "operator-token"}, &account)
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "admin", Namespace: "team-a", Role: ServiceAccountRoleAdmin, Token: "admin-token"}, &account)
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1"}, &record)

	var assignments struct {
		Assignments []Assignment `json:"assignments"`
	}
	getJSONAuth(t, server.URL+"/v1/assignments", "viewer-token", &assignments)
	if code := postJSONStatus(t, server.URL+"/v1/assignments", "viewer-token", Assignment{WorkerID: "worker-1", Verb: "noop"}); code != http.StatusForbidden {
		t.Fatalf("viewer assignment POST status = %d, want 403", code)
	}
	var created Assignment
	postJSONAuth(t, server.URL+"/v1/assignments", "operator-token", Assignment{WorkerID: "worker-1", Verb: "noop"}, &created)
	if created.Namespace != "team-a" {
		t.Fatalf("operator assignment namespace = %q, want team-a", created.Namespace)
	}
	if code := postJSONStatus(t, server.URL+"/v1/service-accounts", "operator-token", ServiceAccountRequest{Name: "denied", Token: "denied-token"}); code != http.StatusForbidden {
		t.Fatalf("operator service-account POST status = %d, want 403", code)
	}
	postJSONAuth(t, server.URL+"/v1/service-accounts", "admin-token", ServiceAccountRequest{Name: "next-viewer", Role: ServiceAccountRoleViewer, Token: "next-viewer-token"}, &account)
	if account.ServiceAccount.Namespace != "team-a" || account.ServiceAccount.Role != ServiceAccountRoleViewer {
		t.Fatalf("admin-created account = %+v", account.ServiceAccount)
	}
	if code := postJSONStatus(t, server.URL+"/v1/service-accounts", "", ServiceAccountRequest{Name: "bad-role", Role: "owner", Token: "bad-role-token"}); code != http.StatusBadRequest {
		t.Fatalf("bad role status = %d, want 400", code)
	}
}

func TestHandlerOIDCBindingAuthScopesOperator(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	key, keyPEM := testOIDCKey(t)
	store := NewMemoryStore(time.Minute)
	store.now = func() time.Time { return now }
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var binding OIDCBindingResult
	postJSON(t, server.URL+"/v1/oidc-bindings", OIDCBindingRequest{
		Name:      "github-main",
		Issuer:    "https://token.actions.githubusercontent.com",
		Subject:   "repo:tmc/cove:ref:refs/heads/main",
		Audience:  "cove-fleet",
		Namespace: "team-a",
		Role:      ServiceAccountRoleOperator,
		Keys:      []OIDCKey{{KID: "kid-1", PEM: keyPEM}},
	}, &binding)
	if binding.Binding.Name != "github-main" || binding.Binding.Namespace != "team-a" {
		t.Fatalf("binding = %+v", binding.Binding)
	}
	var list struct {
		OIDCBindings []OIDCBinding `json:"oidc_bindings"`
	}
	getJSON(t, server.URL+"/v1/oidc-bindings", &list)
	if len(list.OIDCBindings) != 1 || list.OIDCBindings[0].Name != "github-main" || len(list.OIDCBindings[0].KeyIDs) != 1 {
		t.Fatalf("oidc bindings = %+v", list.OIDCBindings)
	}

	token := signOIDCJWT(t, key, "kid-1", map[string]any{
		"iss": "https://token.actions.githubusercontent.com",
		"sub": "repo:tmc/cove:ref:refs/heads/main",
		"aud": "cove-fleet",
		"exp": now.Add(time.Hour).Unix(),
	})
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}, Capacity: Capacity{MaxVMs: 2}}, &record)
	var created SandboxStatus
	postJSONAuth(t, server.URL+"/v1/sandboxes", token, SandboxRequest{ID: "job-1", ImageRef: "base:v1"}, &created)
	if created.Namespace != "team-a" || created.ID != "job-1" {
		t.Fatalf("created sandbox = %+v, want team-a job-1", created)
	}
	if code := postJSONStatus(t, server.URL+"/v1/sandboxes", token, SandboxRequest{ID: "job-2", Namespace: "team-b", ImageRef: "base:v1"}); code != http.StatusForbidden {
		t.Fatalf("cross-namespace oidc sandbox status = %d, want 403", code)
	}
	if code := postJSONStatus(t, server.URL+"/v1/service-accounts", token, ServiceAccountRequest{Name: "denied", Token: "denied"}); code != http.StatusForbidden {
		t.Fatalf("oidc operator service-account POST status = %d, want 403", code)
	}
	var audit struct {
		Events []AuditEvent `json:"events"`
	}
	getJSON(t, server.URL+"/v1/audit?limit=1", &audit)
	if len(audit.Events) != 1 || audit.Events[0].Actor != "oidc:github-main" || audit.Events[0].TargetID != "job-1" {
		t.Fatalf("audit = %+v, want oidc sandbox create", audit.Events)
	}
}

func TestHandlerWarmPoolNamespaceScope(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var account ServiceAccountResult
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-a", Namespace: "team-a", Token: "token-a"}, &account)
	postJSON(t, server.URL+"/v1/service-accounts", ServiceAccountRequest{Name: "team-b", Namespace: "team-b", Token: "token-b"}, &account)
	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1", ImageRefs: []string{"base:v1"}}, &record)

	var ensured WarmPoolResult
	postJSONAuth(t, server.URL+"/v1/warm-pools", "token-a", WarmPoolRequest{Name: "runner", ImageRef: "base:v1", Size: 1}, &ensured)
	if ensured.Pool.Namespace != "team-a" || ensured.Pool.Name != "runner" {
		t.Fatalf("warm pool = %+v, want team-a runner", ensured.Pool)
	}
	var list struct {
		WarmPools []WarmPoolStatus `json:"warm_pools"`
	}
	getJSONAuth(t, server.URL+"/v1/warm-pools", "token-b", &list)
	if len(list.WarmPools) != 0 {
		t.Fatalf("team-b warm pools = %+v, want none", list.WarmPools)
	}
	if code := getJSONStatus(t, server.URL+"/v1/warm-pools/runner", "token-b"); code != http.StatusNotFound {
		t.Fatalf("cross-namespace warm pool GET status = %d, want 404", code)
	}
}

func TestHandlerReconcileEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(time.Minute)
	store.assignmentTTL = time.Second
	store.now = func() time.Time { return now }
	server := httptest.NewServer(Handler(store))
	defer server.Close()

	var record HostRecord
	postJSON(t, server.URL+"/v1/workers/register", WorkerHeartbeat{ID: "worker-1"}, &record)
	var created Assignment
	postJSON(t, server.URL+"/v1/assignments", Assignment{ID: "assignment-1", Verb: "noop"}, &created)
	var leased Assignment
	getJSON(t, server.URL+"/v1/workers/worker-1/assignments", &leased)
	if leased.Status != "leased" {
		t.Fatalf("leased assignment = %+v", leased)
	}

	store.now = func() time.Time { return now.Add(2 * time.Second) }
	var result ReconcileResult
	postJSON(t, server.URL+"/v1/reconcile", map[string]string{}, &result)
	if len(result.RequeuedAssignments) != 1 || result.RequeuedAssignments[0] != "assignment-1" {
		t.Fatalf("reconcile result = %+v", result)
	}
}

func TestHandlerRejectsMismatchedReportID(t *testing.T) {
	store := NewMemoryStore(time.Minute)
	if _, err := store.UpsertHeartbeat(WorkerHeartbeat{ID: "worker-1"}); err != nil {
		t.Fatal(err)
	}
	reqBody, err := json.Marshal(WorkerReport{ID: "other", Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	Handler(store).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/workers/worker-1/reports", bytes.NewReader(reqBody)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func postJSON(t *testing.T, url string, in, out any) {
	t.Helper()
	postJSONAuth(t, url, "", in, out)
}

func postJSONAuth(t *testing.T, url, token string, in, out any) {
	t.Helper()
	resp := postJSONRequest(t, url, token, in)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func postJSONStatus(t *testing.T, url, token string, in any) int {
	t.Helper()
	resp := postJSONRequest(t, url, token, in)
	defer resp.Body.Close()
	return resp.StatusCode
}

func postJSONRequest(t *testing.T, url, token string, in any) *http.Response {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	getJSONAuth(t, url, "", out)
}

func getJSONAuth(t *testing.T, url, token string, out any) {
	t.Helper()
	resp := getJSONRequest(t, url, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func getJSONStatus(t *testing.T, url, token string) int {
	t.Helper()
	resp := getJSONRequest(t, url, token)
	defer resp.Body.Close()
	return resp.StatusCode
}

func getJSONRequest(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func deleteJSON(t *testing.T, url string, out any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE %s status = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func testOIDCKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	return key, string(pem.EncodeToMemory(block))
}

func testOIDCJWK(key *rsa.PrivateKey, kid string) map[string]string {
	return map[string]string{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
	}
}

func signOIDCJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(headerJSON) + "." + enc.EncodeToString(claimsJSON)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + enc.EncodeToString(sig)
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func auditAction(events []AuditEvent, action string) *AuditEvent {
	for i := range events {
		if events[i].Action == action {
			return &events[i]
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statusByAssignmentID(assignments []Assignment, id string) string {
	for _, assignment := range assignments {
		if assignment.ID == id {
			return assignment.Status
		}
	}
	return ""
}

func skipReason(skipped []ImagePrepareSkip, workerID string) string {
	for _, skip := range skipped {
		if skip.WorkerID == workerID {
			return skip.Reason
		}
	}
	return ""
}

func skipImageGCReason(skipped []ImageGCSkip, workerID string) string {
	for _, skip := range skipped {
		if skip.WorkerID == workerID {
			return skip.Reason
		}
	}
	return ""
}

func skipLifecyclePolicyReason(skipped []LifecyclePolicySkip, workerID string) string {
	for _, skip := range skipped {
		if skip.WorkerID == workerID {
			return skip.Reason
		}
	}
	return ""
}

func skipStoragePolicyReason(skipped []StoragePolicySkip, workerID string) string {
	for _, skip := range skipped {
		if skip.WorkerID == workerID {
			return skip.Reason
		}
	}
	return ""
}
