package fleetcontrol

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
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

func skipReason(skipped []ImagePrepareSkip, workerID string) string {
	for _, skip := range skipped {
		if skip.WorkerID == workerID {
			return skip.Reason
		}
	}
	return ""
}
