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
	if got.Report == nil || got.Report.AssignmentID != "a1" || got.Status != "done" {
		t.Fatalf("report not recorded: %+v", got)
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
