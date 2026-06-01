package agentsandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCloudClientCreateExecControlDelete(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	client, err := Create(ctx, ClientOptions{
		Provider:             ProviderCloud,
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		SandboxID:            "job-1",
		ImageRef:             "base:v1",
		ManifestBundle:       "manifests",
		ImageManifestDigest:  "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ImageDigestRef:       "ghcr.io/me/dev-vm@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ImagePlatform:        "darwin/arm64",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "gui", "ram-overlay", ""},
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.ID() != "job-1" || client.VMName() != "cove-sandbox-job-1" || client.Provider() != ProviderCloud {
		t.Fatalf("client = id %q vm %q provider %q", client.ID(), client.VMName(), client.Provider())
	}
	list, err := client.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "job-1" || list[0].ImageRef != "base:v1" || list[0].ImageManifestDigest == "" || !equalStringSlices(list[0].RequiredCapabilities, []string{"ram-overlay"}) {
		t.Fatalf("List = %+v, want job-1 base:v1", list)
	}
	wait, err := client.Wait(ctx, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !wait.Done || wait.Sandbox.ID != "job-1" {
		t.Fatalf("Wait = %+v, want done job-1", wait)
	}
	lease, err := client.Lease(ctx, "runner-42", 30*time.Second)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if lease.Lease.Holder != "runner-42" || lease.Sandbox.Lease == nil {
		t.Fatalf("Lease = %+v, want runner-42", lease)
	}
	released, err := client.ReleaseLease(ctx, "runner-42")
	if err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if released.Sandbox.Lease != nil {
		t.Fatalf("ReleaseLease = %+v, want no active lease", released)
	}
	if err := client.WaitReady(ctx, time.Second); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if err := client.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	result, err := client.Exec(ctx, ExecRequest{
		Command: []string{"/bin/echo", "ok"},
		Env:     map[string]string{"A": "1"},
		Timeout: 2500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 7 || result.Stdout != "out" || result.Stderr != "err" {
		t.Fatalf("exec result = %+v", result)
	}
	image, err := client.Screenshot(ctx, ScreenshotOptions{Format: "png"})
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if string(image) != "png" {
		t.Fatalf("screenshot = %q, want png", image)
	}
	if err := client.Key(ctx, KeyEvent{KeyCode: 36, Modifiers: 1 << 20}); err != nil {
		t.Fatalf("Key: %v", err)
	}
	if err := client.Text(ctx, "hi"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if err := client.Mouse(ctx, MouseEvent{X: 4, Y: 5, Action: "click", Button: 1, Absolute: true}); err != nil {
		t.Fatalf("Mouse: %v", err)
	}
	metering, err := client.Metering(ctx)
	if err != nil {
		t.Fatalf("Metering: %v", err)
	}
	if metering.Summary.Records != 1 || metering.Records[0].SandboxID != "job-1" {
		t.Fatalf("Metering = %+v, want one job-1 record", metering)
	}
	allMetering, err := client.ListMetering(ctx, "job-1")
	if err != nil {
		t.Fatalf("ListMetering: %v", err)
	}
	if allMetering.Summary.SandboxID != "job-1" {
		t.Fatalf("ListMetering = %+v, want job-1 summary", allMetering)
	}
	if err := client.Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.path != "" && req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{
		"/v1/sandboxes",
		"/v1/sandboxes",
		"/v1/sandboxes/job-1/wait",
		"/v1/sandboxes/job-1/lease",
		"/v1/sandboxes/job-1/lease",
		"/v1/sandboxes/job-1",
		"/v1/sandboxes/job-1/restart",
		"/v1/sandboxes/job-1/exec",
		"/v1/sandboxes/job-1/control",
		"/v1/sandboxes/job-1/control",
		"/v1/sandboxes/job-1/control",
		"/v1/sandboxes/job-1/control",
		"/v1/sandboxes/job-1/control",
		"/v1/sandboxes/job-1/metering",
		"/v1/metering/sandboxes",
		"/v1/sandboxes/job-1",
	}
	if len(paths) != len(wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	for i := range wantPaths {
		if paths[i] != wantPaths[i] {
			t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
		}
	}
	create := server.requests[0].body
	if create["image_ref"] != "base:v1" || create["namespace"] != "team-a" || create["id"] != "job-1" {
		t.Fatalf("create body = %+v", create)
	}
	if create["manifest_bundle"] != "manifests" || create["image_manifest_digest"] == "" || create["image_digest_ref"] == "" || create["image_platform"] != "darwin/arm64" {
		t.Fatalf("create image identity = %+v, want manifest bundle fields", create)
	}
	labels, ok := create["required_labels"].(map[string]any)
	if !ok || labels["zone"] != "desk" {
		t.Fatalf("create required labels = %+v, want zone=desk", create["required_labels"])
	}
	if !equalAnyStringSlice(create["required_capabilities"], []string{"ram-overlay", "gui"}) {
		t.Fatalf("create required capabilities = %+v, want ram-overlay/gui", create["required_capabilities"])
	}
	if server.requests[1].query.Get("namespace") != "team-a" {
		t.Fatalf("list query = %q, want team-a", server.requests[1].query.Encode())
	}
	if server.requests[2].query.Get("timeout") != "2.5s" {
		t.Fatalf("wait query = %q, want timeout=2.5s", server.requests[2].query.Encode())
	}
	if server.requests[3].body["holder"] != "runner-42" || server.requests[3].body["ttl"] != "30s" {
		t.Fatalf("lease body = %+v", server.requests[3].body)
	}
	if server.requests[4].query.Get("holder") != "runner-42" {
		t.Fatalf("release query = %q, want holder=runner-42", server.requests[4].query.Encode())
	}
	execReq := server.requests[7].body
	if execReq["timeout"] != "2.5s" {
		t.Fatalf("exec timeout = %v, want 2.5s", execReq["timeout"])
	}
	if server.requests[14].query.Get("sandbox_id") != "job-1" || server.requests[14].query.Get("namespace") != "team-a" {
		t.Fatalf("metering query = %q, want namespace/team sandbox", server.requests[14].query.Encode())
	}
	control := server.controlRequests()
	if control[0].body["type"] != "screenshot" {
		t.Fatalf("first control = %+v, want screenshot", control[0].body)
	}
	if control[1].body["key"].(map[string]any)["key_down"] != true || control[2].body["key"].(map[string]any)["key_down"] != false {
		t.Fatalf("key controls = %+v %+v", control[1].body, control[2].body)
	}
	if control[3].body["text"].(map[string]any)["text"] != "hi" {
		t.Fatalf("text control = %+v", control[3].body)
	}
	if control[4].body["mouse"].(map[string]any)["absolute"] != true {
		t.Fatalf("mouse control = %+v", control[4].body)
	}
}

func TestCloudClientPlansSandboxPlacement(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	plan, err := Plan(ctx, ClientOptions{
		Provider:             ProviderCloud,
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		ImageRef:             "base:v1",
		ManifestBundle:       "manifests",
		ImagePlatform:        "darwin/arm64",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif", ""},
		PlacementLimit:       3,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID != "placement-plan-1" || len(plan.Candidates) != 1 || plan.Candidates[0].WorkerID != "worker-1" {
		t.Fatalf("plan = %+v, want one worker-1 candidate", plan)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].WorkerID != "worker-2" || plan.Skipped[0].Reason != "capability" {
		t.Fatalf("plan skipped = %+v, want worker-2 capability skip", plan.Skipped)
	}
	req := server.requests[0]
	if req.path != "/v1/placements/plan" || req.authorization != "Bearer secret" {
		t.Fatalf("request = %+v, want authorized placement plan", req)
	}
	if req.body["namespace"] != "team-a" || req.body["image_ref"] != "base:v1" || req.body["manifest_bundle"] != "manifests" || req.body["image_platform"] != "darwin/arm64" || req.body["limit"] != float64(3) {
		t.Fatalf("plan body = %+v, want image identity and limit", req.body)
	}
	labels, ok := req.body["required_labels"].(map[string]any)
	if !ok || labels["zone"] != "desk" {
		t.Fatalf("required labels = %+v, want zone=desk", req.body["required_labels"])
	}
	if !equalAnyStringSlice(req.body["required_capabilities"], []string{"ram-overlay", "asif"}) {
		t.Fatalf("required capabilities = %+v, want ram-overlay/asif", req.body["required_capabilities"])
	}
}

func TestCloudClientRejectsNegativePlacementLimit(t *testing.T) {
	ctx := context.Background()
	_, err := Plan(ctx, ClientOptions{
		Provider:       ProviderCloud,
		FleetURL:       "https://fleet.example",
		APIKey:         "secret",
		ImageRef:       "base:v1",
		PlacementLimit: -1,
	})
	if err == nil || !strings.Contains(err.Error(), "placement limit must be non-negative") {
		t.Fatalf("negative placement limit err = %v, want validation error", err)
	}
}

func TestCloudClientPassesLeaseHolderToMutations(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	client, err := NewClient(ClientOptions{
		Provider:  ProviderCloud,
		FleetURL:  server.URL,
		SandboxID: "job-1",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lease(ctx, "runner-42", time.Second); err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if err := client.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if _, err := client.Exec(ctx, ExecRequest{Command: []string{"true"}, Timeout: time.Second}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if _, err := client.Screenshot(ctx, ScreenshotOptions{Format: "png"}); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if err := client.Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := server.requests[1].body["holder"]; got != "runner-42" {
		t.Fatalf("restart holder = %v, want runner-42", got)
	}
	if got := server.requests[2].body["holder"]; got != "runner-42" {
		t.Fatalf("exec holder = %v, want runner-42", got)
	}
	if got := server.requests[3].body["holder"]; got != "runner-42" {
		t.Fatalf("control holder = %v, want runner-42", got)
	}
	if got := server.requests[4].query.Get("holder"); got != "runner-42" {
		t.Fatalf("delete holder = %q, want runner-42", got)
	}
}

func TestCloudClientListFilters(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	client, err := NewClient(ClientOptions{
		Provider:  ProviderCloud,
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		SandboxID: "job-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListPage(ctx, SandboxListOptions{
		Status:   "ready",
		WorkerID: "worker-1",
		ImageRef: "base:v1",
		Offset:   2,
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("ListPage filtered: %v", err)
	}
	list := page.Sandboxes
	if len(list) != 1 || list[0].ID != "job-1" {
		t.Fatalf("ListPage filtered = %+v, want job-1", page)
	}
	if page.Offset != 2 || page.Limit != 5 || page.Count != 1 {
		t.Fatalf("ListPage metadata = %+v, want offset 2 limit 5 count 1", page)
	}
	query := server.requests[len(server.requests)-1].query
	if query.Get("namespace") != "team-a" || query.Get("status") != "ready" || query.Get("worker_id") != "worker-1" || query.Get("image_ref") != "base:v1" || query.Get("offset") != "2" || query.Get("limit") != "5" {
		t.Fatalf("filtered list query = %q", query.Encode())
	}
	if _, err := client.List(ctx, SandboxListOptions{Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("negative limit err = %v, want validation error", err)
	}
	if _, err := client.List(ctx, SandboxListOptions{Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("negative offset err = %v, want validation error", err)
	}
}

func TestCloudClientEvents(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	client, err := NewClient(ClientOptions{
		Provider:  ProviderCloud,
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		SandboxID: "job-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Events(ctx, SandboxEventListOptions{
		Actor:  "service-account:ci",
		Action: "sandbox.exec",
		Offset: 2,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if page.Count != 1 || page.Offset != 2 || page.Limit != 5 || len(page.Events) != 1 || page.Events[0].Action != "sandbox.exec" {
		t.Fatalf("Events = %+v, want sandbox.exec page", page)
	}
	query := server.requests[len(server.requests)-1].query
	if query.Get("actor") != "service-account:ci" || query.Get("action") != "sandbox.exec" || query.Get("offset") != "2" || query.Get("limit") != "5" {
		t.Fatalf("events query = %q", query.Encode())
	}
	if _, err := client.Events(ctx, SandboxEventListOptions{Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("negative event limit err = %v, want validation error", err)
	}
	if _, err := client.Events(ctx, SandboxEventListOptions{Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("negative event offset err = %v, want validation error", err)
	}
}

func TestCloudClientReports(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	client, err := NewClient(ClientOptions{
		Provider:  ProviderCloud,
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		SandboxID: "job-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Reports(ctx, SandboxReportListOptions{
		Role:   "exec",
		Status: "complete",
		Offset: 2,
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Reports: %v", err)
	}
	if page.Count != 1 || page.Offset != 2 || page.Limit != 5 || len(page.Reports) != 1 || page.Reports[0].Report.Stdout != "out" {
		t.Fatalf("Reports = %+v, want exec report page", page)
	}
	query := server.requests[len(server.requests)-1].query
	if query.Get("role") != "exec" || query.Get("status") != "complete" || query.Get("offset") != "2" || query.Get("limit") != "5" {
		t.Fatalf("reports query = %q", query.Encode())
	}
	if _, err := client.Reports(ctx, SandboxReportListOptions{Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("negative report limit err = %v, want validation error", err)
	}
	if _, err := client.Reports(ctx, SandboxReportListOptions{Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("negative report offset err = %v, want validation error", err)
	}
}

func TestExecResultCheck(t *testing.T) {
	err := (ExecResult{ExitCode: 2, Stderr: "nope\n"}).Check()
	if err == nil || err.Error() != "guest command exited 2: nope" {
		t.Fatalf("Check error = %v", err)
	}
}

func TestCloudWriteFileUsesPortableBase64Decode(t *testing.T) {
	server := newSDKFleetServer(t)
	client, err := NewClient(ClientOptions{
		Provider:  ProviderCloud,
		FleetURL:  server.URL,
		SandboxID: "job-1",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WriteFile(context.Background(), "/tmp/hello.txt", []byte("hello"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if len(server.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(server.requests))
	}
	command, ok := server.requests[0].body["command"].([]any)
	if !ok || len(command) != 3 {
		t.Fatalf("command body = %+v", server.requests[0].body["command"])
	}
	script, ok := command[2].(string)
	if !ok {
		t.Fatalf("command script = %T", command[2])
	}
	for _, want := range []string{"/usr/bin/base64 -D", "/usr/bin/base64 -d", "chmod 600 '/tmp/hello.txt'"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

type sdkFleetServer struct {
	*httptest.Server
	requests []sdkRequest
}

type sdkRequest struct {
	method        string
	path          string
	query         url.Values
	authorization string
	body          map[string]any
}

func newSDKFleetServer(t *testing.T) *sdkFleetServer {
	t.Helper()
	server := &sdkFleetServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		req := sdkRequest{
			method:        r.Method,
			path:          r.URL.Path,
			query:         r.URL.Query(),
			authorization: r.Header.Get("authorization"),
			body:          readSDKBody(t, r),
		}
		server.requests = append(server.requests, req)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/placements/plan":
			writeSDKJSON(t, w, PlacementPlan{
				ID:                   "placement-plan-1",
				Namespace:            "team-a",
				Policy:               "image-affinity",
				ImageRef:             "base:v1",
				ImagePlatform:        "darwin/arm64",
				RequiredLabels:       map[string]string{"zone": "desk"},
				RequiredCapabilities: []string{"ram-overlay", "asif"},
				Limit:                3,
				Candidates:           []PlacementCandidate{{Rank: 1, WorkerID: "worker-1", Load: 1, MaxVMs: 4, RequestedVMs: 1, HasImage: true}},
				Skipped:              []PlacementSkip{{WorkerID: "worker-2", Reason: "capability", MissingCapabilities: []string{"asif"}}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			writeSDKJSON(t, w, SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", RequiredCapabilities: []string{"ram-overlay"}, Status: "pending"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes":
			writeSDKJSON(t, w, map[string]any{
				"sandboxes": []SandboxStatus{{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", ImageRef: "base:v1", ImageManifestDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111", ImageDigestRef: "ghcr.io/me/dev-vm@sha256:1111111111111111111111111111111111111111111111111111111111111111", ImagePlatform: "darwin/arm64", RequiredCapabilities: []string{"ram-overlay"}, Status: "ready"}},
				"count":     1,
				"offset":    atoiDefault(r.URL.Query().Get("offset"), 0),
				"limit":     atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/wait":
			writeSDKJSON(t, w, WaitResult{Done: true, Sandbox: SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "ready"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/lease":
			lease := Lease{Holder: "runner-42", Expires: time.Now().Add(time.Minute)}
			writeSDKJSON(t, w, LeaseResult{Sandbox: SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "ready", Lease: &lease}, Lease: lease})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/job-1/lease":
			writeSDKJSON(t, w, LeaseResult{Sandbox: SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "ready"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/job-1":
			writeSDKJSON(t, w, SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", RequiredCapabilities: []string{"ram-overlay"}, Status: "ready"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/restart":
			writeSDKJSON(t, w, SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "restarting"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/exec":
			command, _ := req.body["command"].([]any)
			if len(command) > 0 && command[0] == "/bin/echo" {
				writeSDKJSON(t, w, map[string]any{"done": true, "exit_code": 7, "stdout": "out", "stderr": "err"})
				return
			}
			writeSDKJSON(t, w, map[string]any{"done": true, "exit_code": 0})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/control":
			if req.body["type"] == "screenshot" {
				writeSDKJSON(t, w, map[string]any{"done": true, "data": base64.StdEncoding.EncodeToString([]byte("png")), "response": map[string]any{"success": true}})
				return
			}
			writeSDKJSON(t, w, map[string]any{"done": true, "response": map[string]any{"success": true}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/job-1/metering":
			writeSDKJSON(t, w, sdkMetering("job-1"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/job-1/events":
			writeSDKJSON(t, w, SandboxEventListResult{
				Events: []SandboxEvent{{
					ID:           "audit-1",
					Time:         time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
					Namespace:    "team-a",
					Actor:        "service-account:ci",
					Action:       "sandbox.exec",
					TargetType:   "sandbox",
					TargetID:     "job-1",
					AssignmentID: "assignment-1",
					Fields:       map[string]string{"argc": "1"},
				}},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/job-1/reports":
			writeSDKJSON(t, w, SandboxReportListResult{
				Reports: []SandboxReport{{
					Namespace:    "team-a",
					SandboxID:    "job-1",
					AssignmentID: "assignment-1",
					Role:         "exec",
					WorkerID:     "worker-1",
					Status:       "complete",
					Report:       WorkerReport{AssignmentID: "assignment-1", Status: "complete", ExitCode: 7, Stdout: "out", Stderr: "err"},
				}},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/metering/sandboxes":
			writeSDKJSON(t, w, sdkMetering("job-1"))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/job-1":
			writeSDKJSON(t, w, SandboxStatus{Namespace: "team-a", ID: "job-1", Status: "draining"})
		default:
			http.NotFound(w, r)
		}
	})
	server.Server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func (s *sdkFleetServer) controlRequests() []sdkRequest {
	var out []sdkRequest
	for _, req := range s.requests {
		if req.path == "/v1/sandboxes/job-1/control" {
			out = append(out, req)
		}
	}
	return out
}

func sdkMetering(id string) MeteringResult {
	return MeteringResult{
		Records: []MeteringRecord{{ID: "metering-1", SandboxID: id, AssignmentID: "assignment-1", Status: "ready", DurationMillis: 1000, VMMillis: 1000}},
		Summary: MeteringSummary{SandboxID: id, Records: 1, DurationMillis: 1000, VMMillis: 1000},
	}
}

func atoiDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func equalStringSlices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalAnyStringSlice(got any, want []string) bool {
	items, ok := got.([]any)
	if !ok || len(items) != len(want) {
		return false
	}
	for i := range want {
		value, ok := items[i].(string)
		if !ok || value != want[i] {
			return false
		}
	}
	return true
}

func readSDKBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	if r.Body == nil || r.ContentLength == 0 {
		return map[string]any{}
	}
	defer r.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return body
}

func writeSDKJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	w.Header().Set("content-type", "application/json")
	w.Header().Set("content-length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}
