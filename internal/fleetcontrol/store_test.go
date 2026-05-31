package fleetcontrol

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if event := auditAction(reopened.ListAudit(0), "assignment.create"); event == nil || event.AssignmentID != created.ID {
		t.Fatalf("reopened audit missing assignment create: %+v", reopened.ListAudit(0))
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
	if result.Pool.Policy != PolicyImageAffinity || result.Pool.Active != 2 {
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
	if len(result.Created) != 0 || result.Pool.Active != 2 {
		t.Fatalf("second ensure = %+v", result)
	}
	reopened, err := OpenStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pools := reopened.ListWarmPools()
	if len(pools) != 1 || pools[0].Name != "runner" || pools[0].Active != 2 {
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
	if len(pools) != 1 || pools[0].Active != 1 || pools[0].Assignments[0].ID == first {
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
	if len(pools) != 1 || pools[0].Active != 1 || pools[0].Assignments[0].ID == slotID {
		t.Fatalf("pools after claim = %+v, want replenished active replacement", pools)
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
	if len(pools) != 1 || pools[0].Active != 0 {
		t.Fatalf("pools after capacity-bound claim = %+v, want no replacement", pools)
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
	if len(pools) != 1 || pools[0].Active != 1 {
		t.Fatalf("pools after downsize = %+v, want active 1", pools)
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
	if len(result.Canceled) != 2 || len(result.Cleanup) != 0 || result.Pool.Active != 0 {
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
	if result.Pool.Name != "runner" || result.Pool.Active != 1 || len(result.Created) != 1 {
		t.Fatalf("warm pool result = %+v", result)
	}
	if result.Created[0].WorkerID != "worker-1" || result.Created[0].WarmPool != "runner" {
		t.Fatalf("created assignment = %+v", result.Created[0])
	}

	var list struct {
		WarmPools []WarmPoolStatus `json:"warm_pools"`
	}
	getJSON(t, server.URL+"/v1/warm-pools", &list)
	if len(list.WarmPools) != 1 || list.WarmPools[0].Name != "runner" || list.WarmPools[0].Active != 1 {
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
	if len(result.Canceled) != 2 || len(result.Cleanup) != 0 || result.Pool.Active != 0 {
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
	if status.Name != "runner" || status.Active != 2 {
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
	resp, err := http.Get(server.URL + "/v1/audit?limit=bad")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad audit limit status = %d, want 400", resp.StatusCode)
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
