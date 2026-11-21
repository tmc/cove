package coved

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tmc/cove/internal/fleetwire"
)

func TestFleetWorkerMergesDiscoveredCapabilities(t *testing.T) {
	previous := discoverFleetCapabilities
	discoverFleetCapabilities = func() []string {
		return []string{FleetCapabilityRAMOverlay, FleetCapabilityRAMOverlay, FleetCapabilityASIF}
	}
	t.Cleanup(func() {
		discoverFleetCapabilities = previous
	})

	worker, err := NewFleetWorker(FleetWorkerConfig{
		ControllerURL: "http://127.0.0.1:9758",
		ID:            "worker-1",
		Capabilities:  []string{FleetCapabilityAPFSQuota, ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(worker.heartbeat().Capabilities, ","); got != "apfs-quota,asif,ram-overlay" {
		t.Fatalf("heartbeat capabilities = %q, want apfs-quota,asif,ram-overlay", got)
	}
}

func TestDefaultFleetCapabilitiesForDarwin(t *testing.T) {
	got := defaultFleetCapabilitiesFor("darwin", func() bool { return true }, func() bool { return true }, func() int { return 0 })
	if strings.Join(got, ",") != "ram-overlay,asif,apfs-quota" {
		t.Fatalf("capabilities = %+v, want ram-overlay/asif/apfs-quota", got)
	}
}

func TestDefaultFleetCapabilitiesRequiresAvailableProbes(t *testing.T) {
	got := defaultFleetCapabilitiesFor("darwin", func() bool { return false }, func() bool { return true }, func() int { return 501 })
	if strings.Join(got, ",") != "ram-overlay" {
		t.Fatalf("capabilities = %+v, want ram-overlay only without ASIF/root quota", got)
	}
	if got := defaultFleetCapabilitiesFor("linux", func() bool { return true }, func() bool { return true }, func() int { return 0 }); len(got) != 0 {
		t.Fatalf("linux capabilities = %+v, want none", got)
	}
}

func TestFleetWorkerHTTPContract(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/workers/register":
			var heartbeat fleetwire.WorkerHeartbeat
			if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil || heartbeat.ID != "worker" {
				t.Errorf("register payload = %+v, error = %v", heartbeat, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		case "/v1/workers/worker/assignments":
			json.NewEncoder(w).Encode(fleetwire.Assignment{ID: "job", Verb: "noop"})
			return
		case "/v1/workers/worker/reports":
			var report fleetwire.WorkerReport
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil || report.ID != "worker" || report.AssignmentID != "job" || report.Status != "complete" {
				t.Errorf("report payload = %+v, error = %v", report, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(fleetwire.HostRecord{ID: "worker"})
	}))
	worker, err := NewFleetWorker(FleetWorkerConfig{ControllerURL: server.URL, ID: "worker"})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	defer server.Close()
	ctx := context.Background()
	if err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollAssignment(ctx); err != nil {
		t.Fatal(err)
	}
	server.Close()
	want := []string{"POST /v1/workers/register", "GET /v1/workers/worker/assignments", "POST /v1/workers/worker/reports"}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(requests, want) {
		t.Errorf("worker requests = %v, want %v", requests, want)
	}
}
