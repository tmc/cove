package agentsandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResponseErrorIncludesPlacementPlanID(t *testing.T) {
	resp := &http.Response{
		Status:     "400 Bad Request",
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":"no ready worker matches assignment","placement_plan":{"id":"placement-plan-1"}}`)),
	}
	err := responseError(http.MethodPost, "/v1/sandboxes", resp)
	if err == nil || !strings.Contains(err.Error(), "placement_plan=placement-plan-1") {
		t.Fatalf("responseError = %v, want placement plan id", err)
	}
}

func TestResponseErrorIncludesSandboxCapDiagnostics(t *testing.T) {
	resp := &http.Response{
		Status:     "400 Bad Request",
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":"sandbox namespace \"team-a\" has 1 active sandboxes, max_active_sandboxes is 1","active_count":1,"max_active_sandboxes":1}`)),
	}
	err := responseError(http.MethodPost, "/v1/sandboxes", resp)
	if err == nil || !strings.Contains(err.Error(), "active_sandboxes=1 max_active_sandboxes=1") {
		t.Fatalf("responseError = %v, want sandbox cap diagnostics", err)
	}
}

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
		MaxActiveSandboxes:   3,
		Priority:             5,
		QueueTTL:             45 * time.Second,
		RunTimeout:           5 * time.Minute,
		MaxAttempts:          3,
		RetryDelay:           20 * time.Second,
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
	if list[0].QueueExpires.IsZero() || list[0].QueueAgeMillis != 1500 || list[0].QueueRemainingMillis != 8500 {
		t.Fatalf("List queue diagnostics = %+v, want expires with 1500/8500ms", list[0])
	}
	if list[0].MaxAttempts != 3 || list[0].Attempt != 1 || list[0].RetryDelay != "20s" || list[0].RetryAt.IsZero() || list[0].RetryRemainingMillis != 12000 {
		t.Fatalf("List retry diagnostics = %+v, want attempt 1/3 with 12000ms remaining", list[0])
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
	restart, err := client.RestartResult(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restart.Status != "restarting" || !equalStringSlices(restart.CanceledAssignments, []string{"exec-1"}) {
		t.Fatalf("RestartResult = %+v, want restarting with canceled exec-1", restart)
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
	deleted, err := client.DeleteResult(ctx)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted.Status != "draining" || !equalStringSlices(deleted.CanceledAssignments, []string{"exec-1", "control-1"}) {
		t.Fatalf("DeleteResult = %+v, want draining with canceled sandbox work", deleted)
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
		"/v1/sandboxes/job-1/wait",
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
	if create["max_active_sandboxes"] != float64(3) {
		t.Fatalf("create max active sandboxes = %+v, want 3", create["max_active_sandboxes"])
	}
	if create["priority"] != float64(5) {
		t.Fatalf("create priority = %+v, want 5", create["priority"])
	}
	if create["queue_ttl"] != "45s" {
		t.Fatalf("create queue ttl = %+v, want 45s", create["queue_ttl"])
	}
	if create["run_timeout"] != "300s" {
		t.Fatalf("create run timeout = %+v, want 300s", create["run_timeout"])
	}
	if create["max_attempts"] != float64(3) {
		t.Fatalf("create max attempts = %+v, want 3", create["max_attempts"])
	}
	if create["retry_delay"] != "20s" {
		t.Fatalf("create retry delay = %+v, want 20s", create["retry_delay"])
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
	if server.requests[5].query.Get("timeout") != "1s" || server.requests[5].query.Get("status") != "ready" {
		t.Fatalf("wait ready query = %q, want status=ready timeout=1s", server.requests[5].query.Encode())
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

func TestCloudClientLifecycleResultHelpers(t *testing.T) {
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
	started, err := client.StartResult(ctx)
	if err != nil {
		t.Fatalf("StartResult: %v", err)
	}
	if !started.Started || started.Status != "pending" || started.ID != "job-1" {
		t.Fatalf("StartResult = %+v, want pending started job-1", started)
	}
	stopped, err := client.StopResult(ctx)
	if err != nil {
		t.Fatalf("StopResult: %v", err)
	}
	if stopped.Status != "draining" || stopped.Cleanup == nil || !equalStringSlices(stopped.CanceledAssignments, []string{"exec-1", "control-1"}) {
		t.Fatalf("StopResult = %+v, want draining cleanup with canceled work", stopped)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
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

	page, err := ListPlacementPlans(ctx, PlacementPlanListOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		Policy:    "image-affinity",
		ImageRef:  "base:v1",
		Offset:    1,
		Limit:     2,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ListPlacementPlans: %v", err)
	}
	if page.Count != 1 || page.Offset != 1 || page.Limit != 2 || len(page.Plans) != 1 || page.Plans[0].ID != "placement-plan-1" {
		t.Fatalf("ListPlacementPlans = %+v, want placement-plan-1 page", page)
	}
	got, err := GetPlacementPlan(ctx, PlacementPlanGetOptions{
		FleetURL: server.URL,
		APIKey:   "secret",
		ID:       "placement-plan-1",
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("GetPlacementPlan: %v", err)
	}
	if got.ID != "placement-plan-1" || got.Skipped[0].MissingCapabilities[0] != "asif" {
		t.Fatalf("GetPlacementPlan = %+v, want placement-plan-1 diagnostics", got)
	}
	if len(server.requests) != 3 || server.requests[1].path != "/v1/placements/plans" || server.requests[2].path != "/v1/placements/plans/placement-plan-1" {
		t.Fatalf("placement paths = %+v", server.requests)
	}
	if query := server.requests[1].query; query.Get("namespace") != "team-a" || query.Get("policy") != "image-affinity" || query.Get("image_ref") != "base:v1" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("placement plan list query = %q", query.Encode())
	}
}

func TestCloudClientCreateAndPlanValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := Create(ctx, ClientOptions{
		Provider:           ProviderCloud,
		FleetURL:           "https://fleet.example",
		APIKey:             "secret",
		ImageRef:           "base:v1",
		MaxActiveSandboxes: -1,
	}); err == nil || !strings.Contains(err.Error(), "max active sandboxes must be non-negative") {
		t.Fatalf("negative max active sandboxes err = %v, want validation error", err)
	}
	if _, err := Create(ctx, ClientOptions{
		Provider: ProviderCloud,
		FleetURL: "https://fleet.example",
		APIKey:   "secret",
		ImageRef: "base:v1",
		Priority: -1,
	}); err == nil || !strings.Contains(err.Error(), "priority must be non-negative") {
		t.Fatalf("negative priority err = %v, want validation error", err)
	}
	if _, err := Create(ctx, ClientOptions{
		Provider: ProviderCloud,
		FleetURL: "https://fleet.example",
		APIKey:   "secret",
		ImageRef: "base:v1",
		QueueTTL: -time.Second,
	}); err == nil || !strings.Contains(err.Error(), "queue ttl must not be negative") {
		t.Fatalf("negative queue ttl err = %v, want validation error", err)
	}
	if _, err := Create(ctx, ClientOptions{
		Provider:   ProviderCloud,
		FleetURL:   "https://fleet.example",
		APIKey:     "secret",
		ImageRef:   "base:v1",
		RunTimeout: -time.Second,
	}); err == nil || !strings.Contains(err.Error(), "run timeout must not be negative") {
		t.Fatalf("negative run timeout err = %v, want validation error", err)
	}
	if _, err := Create(ctx, ClientOptions{
		Provider:    ProviderCloud,
		FleetURL:    "https://fleet.example",
		APIKey:      "secret",
		ImageRef:    "base:v1",
		MaxAttempts: -1,
	}); err == nil || !strings.Contains(err.Error(), "max attempts must be non-negative") {
		t.Fatalf("negative max attempts err = %v, want validation error", err)
	}
	if _, err := Create(ctx, ClientOptions{
		Provider:   ProviderCloud,
		FleetURL:   "https://fleet.example",
		APIKey:     "secret",
		ImageRef:   "base:v1",
		RetryDelay: -time.Second,
	}); err == nil || !strings.Contains(err.Error(), "retry delay must not be negative") {
		t.Fatalf("negative retry delay err = %v, want validation error", err)
	}
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
	if _, err := ListPlacementPlans(ctx, PlacementPlanListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Limit: -1}); err == nil || !strings.Contains(err.Error(), "placement plan limit must be non-negative") {
		t.Fatalf("negative placement plan limit err = %v, want validation error", err)
	}
	if _, err := ListPlacementPlans(ctx, PlacementPlanListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Offset: -1}); err == nil || !strings.Contains(err.Error(), "placement plan offset must be non-negative") {
		t.Fatalf("negative placement plan offset err = %v, want validation error", err)
	}
	if _, err := GetPlacementPlan(ctx, PlacementPlanGetOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "placement plan id required") {
		t.Fatalf("missing placement plan id err = %v, want validation error", err)
	}
}

func TestCloudClientImagePreparation(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	result, err := PrepareImage(ctx, ImagePrepareOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		ImageRef:             "base:v1",
		ManifestBundle:       "manifests",
		ImageManifestDigest:  "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ImageDigestRef:       "ghcr.io/me/base@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ImagePlatform:        "darwin/arm64",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif", ""},
		Force:                true,
		DryRun:               true,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || result.ID != "image-prepare-1" || len(result.Assignments) != 1 || result.Assignments[0].WorkerID != "worker-1" {
		t.Fatalf("PrepareImage = %+v, want image-prepare-1 worker-1", result)
	}
	if len(result.Skipped) != 3 {
		t.Fatalf("PrepareImage skipped = %+v, want present/label/capability skips", result.Skipped)
	}
	if skip := result.Skipped[0]; skip.WorkerID != "worker-2" || skip.Reason != "present" {
		t.Fatalf("PrepareImage first skip = %+v, want worker-2 present", skip)
	}
	if skip := result.Skipped[1]; skip.WorkerID != "worker-3" || skip.Reason != "label" || skip.MissingLabels["zone"] != "desk" {
		t.Fatalf("PrepareImage label skip = %+v, want worker-3 missing zone", skip)
	}
	if skip := result.Skipped[2]; skip.WorkerID != "worker-4" || skip.Reason != "capability" || !equalStringSlices(skip.MissingCapabilities, []string{"asif"}) {
		t.Fatalf("PrepareImage capability skip = %+v, want worker-4 missing asif", skip)
	}
	page, err := ListImagePreparations(ctx, ImagePrepareListOptions{
		FleetURL:            server.URL,
		APIKey:              "secret",
		Namespace:           "team-a",
		SourceRef:           "ghcr.io/me/base@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ImageRef:            "base:v1",
		ImageManifestDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		Offset:              2,
		Limit:               5,
		Timeout:             time.Second,
	})
	if err != nil {
		t.Fatalf("ListImagePreparations: %v", err)
	}
	if page.Count != 1 || page.Offset != 2 || page.Limit != 5 || len(page.Preparations) != 1 || page.Preparations[0].ID != "image-prepare-1" {
		t.Fatalf("ListImagePreparations = %+v, want one image-prepare-1 page", page)
	}
	got, err := GetImagePreparation(ctx, ImagePrepareGetOptions{
		FleetURL: server.URL,
		APIKey:   "secret",
		ID:       "image-prepare-1",
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("GetImagePreparation: %v", err)
	}
	if got.ID != "image-prepare-1" || got.ImageDigestRef == "" || got.ImagePlatform != "darwin/arm64" {
		t.Fatalf("GetImagePreparation = %+v, want retained image identity", got)
	}

	paths := []string{server.requests[0].path, server.requests[1].path, server.requests[2].path}
	wantPaths := []string{"/v1/images/prepare", "/v1/images/preparations", "/v1/images/preparations/image-prepare-1"}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	for _, req := range server.requests[:3] {
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	body := server.requests[0].body
	if body["namespace"] != "team-a" || body["image_ref"] != "base:v1" || body["manifest_bundle"] != "manifests" || body["force"] != true || body["dry_run"] != true {
		t.Fatalf("prepare body = %+v, want image identity and force", body)
	}
	if _, ok := body["source_ref"]; ok {
		t.Fatalf("prepare source_ref = %v, want manifest bundle to resolve source", body["source_ref"])
	}
	if body["image_manifest_digest"] == "" || body["image_digest_ref"] == "" || body["image_platform"] != "darwin/arm64" {
		t.Fatalf("prepare digest body = %+v, want digest identity", body)
	}
	labels, ok := body["required_labels"].(map[string]any)
	if !ok || labels["zone"] != "desk" {
		t.Fatalf("prepare labels = %+v, want zone=desk", body["required_labels"])
	}
	if !equalAnyStringSlice(body["required_capabilities"], []string{"ram-overlay", "asif"}) {
		t.Fatalf("prepare capabilities = %+v, want ram-overlay/asif", body["required_capabilities"])
	}
	query := server.requests[1].query
	if query.Get("namespace") != "team-a" || query.Get("source_ref") == "" || query.Get("image_ref") != "base:v1" || query.Get("image_manifest_digest") == "" || query.Get("offset") != "2" || query.Get("limit") != "5" {
		t.Fatalf("prepare list query = %q", query.Encode())
	}
}

func TestCloudClientImagePreparationValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := PrepareImage(ctx, ImagePrepareOptions{FleetURL: "https://fleet.example", APIKey: "secret", ImageRef: "base:v1"}); err == nil || !strings.Contains(err.Error(), "source ref or manifest bundle required") {
		t.Fatalf("PrepareImage missing source err = %v, want validation error", err)
	}
	if _, err := PrepareImage(ctx, ImagePrepareOptions{FleetURL: "https://fleet.example", APIKey: "secret", SourceRef: "ghcr.io/me/base:latest"}); err == nil || !strings.Contains(err.Error(), "image ref required") {
		t.Fatalf("PrepareImage missing image err = %v, want validation error", err)
	}
	if _, err := ListImagePreparations(ctx, ImagePrepareListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("ListImagePreparations negative limit err = %v, want validation error", err)
	}
	if _, err := ListImagePreparations(ctx, ImagePrepareListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("ListImagePreparations negative offset err = %v, want validation error", err)
	}
	if _, err := GetImagePreparation(ctx, ImagePrepareGetOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("GetImagePreparation missing id err = %v, want validation error", err)
	}
}

func TestCloudClientMaintenanceRuns(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	apply := true
	clear := false
	warn := 70
	hard := 90

	gc, err := PushImageGC(ctx, ImageGCOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif", ""},
		OlderThan:            "168h",
		Apply:                true,
		DryRun:               true,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatalf("PushImageGC: %v", err)
	}
	if gc.ID != "image-gc-1" || !gc.Apply || !gc.DryRun || gc.Assignments[0].WorkerID != "worker-1" || gc.Skipped[0].Reason != "status" || gc.Skipped[0].Status != "cordoned" {
		t.Fatalf("PushImageGC = %+v, want dry-run plan", gc)
	}
	if len(gc.Skipped) != 3 || gc.Skipped[1].MissingLabels["zone"] != "desk" || !equalStringSlices(gc.Skipped[2].MissingCapabilities, []string{"asif"}) {
		t.Fatalf("PushImageGC skipped = %+v, want structured selector skips", gc.Skipped)
	}
	gcPage, err := ListImageGCRuns(ctx, ImageGCListOptions{FleetURL: server.URL, APIKey: "secret", Namespace: "team-a", OlderThan: "168h", Apply: &apply, Offset: 1, Limit: 2, Timeout: time.Second})
	if err != nil {
		t.Fatalf("ListImageGCRuns: %v", err)
	}
	if gcPage.Count != 1 || gcPage.Runs[0].ID != "image-gc-1" {
		t.Fatalf("ListImageGCRuns = %+v, want image-gc-1", gcPage)
	}
	gotGC, err := GetImageGCRun(ctx, ImageGCGetOptions{FleetURL: server.URL, APIKey: "secret", ID: "image-gc-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetImageGCRun: %v", err)
	}
	if gotGC.ID != "image-gc-1" || gotGC.OlderThan != "168h" {
		t.Fatalf("GetImageGCRun = %+v, want image-gc-1", gotGC)
	}

	policy, err := PushLifecyclePolicy(ctx, LifecyclePolicyOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		VMName:               "ci-runner",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif"},
		IdleTimeout:          "30m",
		RunBudget:            100,
		DryRun:               true,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatalf("PushLifecyclePolicy: %v", err)
	}
	if policy.ID != "lifecycle-policy-1" || policy.VMName != "ci-runner" || policy.RunBudget != 100 || !policy.DryRun {
		t.Fatalf("PushLifecyclePolicy = %+v, want ci-runner policy", policy)
	}
	policyPage, err := ListLifecyclePolicyRuns(ctx, LifecyclePolicyListOptions{FleetURL: server.URL, APIKey: "secret", Namespace: "team-a", VMName: "ci-runner", Clear: &clear, Offset: 1, Limit: 2, Timeout: time.Second})
	if err != nil {
		t.Fatalf("ListLifecyclePolicyRuns: %v", err)
	}
	if policyPage.Count != 1 || policyPage.Runs[0].ID != "lifecycle-policy-1" {
		t.Fatalf("ListLifecyclePolicyRuns = %+v, want lifecycle-policy-1", policyPage)
	}
	gotPolicy, err := GetLifecyclePolicyRun(ctx, LifecyclePolicyGetOptions{FleetURL: server.URL, APIKey: "secret", ID: "lifecycle-policy-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetLifecyclePolicyRun: %v", err)
	}
	if gotPolicy.ID != "lifecycle-policy-1" || gotPolicy.IdleTimeout != "30m" {
		t.Fatalf("GetLifecyclePolicyRun = %+v, want lifecycle-policy-1", gotPolicy)
	}

	budget, err := PushStorageBudget(ctx, StorageBudgetOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay"},
		Target:               "750GB",
		WarnPct:              &warn,
		HardPct:              &hard,
		DryRun:               true,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatalf("PushStorageBudget: %v", err)
	}
	if budget.ID != "storage-budget-1" || budget.Target != "750GB" || budget.WarnPct == nil || *budget.WarnPct != 70 || !budget.DryRun {
		t.Fatalf("PushStorageBudget = %+v, want storage-budget-1", budget)
	}
	budgetPage, err := ListStorageBudgetRuns(ctx, StorageBudgetListOptions{FleetURL: server.URL, APIKey: "secret", Namespace: "team-a", Target: "750GB", Clear: &clear, Offset: 1, Limit: 2, Timeout: time.Second})
	if err != nil {
		t.Fatalf("ListStorageBudgetRuns: %v", err)
	}
	if budgetPage.Count != 1 || budgetPage.Runs[0].ID != "storage-budget-1" {
		t.Fatalf("ListStorageBudgetRuns = %+v, want storage-budget-1", budgetPage)
	}
	gotBudget, err := GetStorageBudgetRun(ctx, StorageBudgetGetOptions{FleetURL: server.URL, APIKey: "secret", ID: "storage-budget-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetStorageBudgetRun: %v", err)
	}
	if gotBudget.ID != "storage-budget-1" || gotBudget.HardPct == nil || *gotBudget.HardPct != 90 {
		t.Fatalf("GetStorageBudgetRun = %+v, want storage-budget-1", gotBudget)
	}

	prune, err := PushStoragePrune(ctx, StoragePruneOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay"},
		Category:             "build-scratch",
		OlderThan:            "48h",
		Apply:                true,
		DryRun:               true,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatalf("PushStoragePrune: %v", err)
	}
	if prune.ID != "storage-prune-1" || !prune.Apply || !prune.DryRun || prune.Category != "build-scratch" {
		t.Fatalf("PushStoragePrune = %+v, want storage-prune-1", prune)
	}
	prunePage, err := ListStoragePruneRuns(ctx, StoragePruneListOptions{FleetURL: server.URL, APIKey: "secret", Namespace: "team-a", Category: "build-scratch", OlderThan: "48h", Apply: &apply, Offset: 1, Limit: 2, Timeout: time.Second})
	if err != nil {
		t.Fatalf("ListStoragePruneRuns: %v", err)
	}
	if prunePage.Count != 1 || prunePage.Runs[0].ID != "storage-prune-1" {
		t.Fatalf("ListStoragePruneRuns = %+v, want storage-prune-1", prunePage)
	}
	gotPrune, err := GetStoragePruneRun(ctx, StoragePruneGetOptions{FleetURL: server.URL, APIKey: "secret", ID: "storage-prune-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetStoragePruneRun: %v", err)
	}
	if gotPrune.ID != "storage-prune-1" || gotPrune.OlderThan != "48h" {
		t.Fatalf("GetStoragePruneRun = %+v, want storage-prune-1", gotPrune)
	}

	runs, err := ListControllerRuns(ctx, ControllerRunListOptions{FleetURL: server.URL, APIKey: "secret", Namespace: "team-a", Kind: "storage.prune", TargetType: "storage", Offset: 1, Limit: 2, Timeout: time.Second})
	if err != nil {
		t.Fatalf("ListControllerRuns: %v", err)
	}
	if runs.Count != 1 || runs.Runs[0].Kind != "storage.prune" || runs.Runs[0].AssignmentCount != 1 {
		t.Fatalf("ListControllerRuns = %+v, want storage prune summary", runs)
	}
	reconcilePlan, err := PlanReconcile(ctx, ReconcileOptions{FleetURL: server.URL, APIKey: "secret", Timeout: time.Second})
	if err != nil {
		t.Fatalf("PlanReconcile: %v", err)
	}
	if !equalStringSlices(reconcilePlan.StaleWorkers, []string{"worker-2"}) || !equalStringSlices(reconcilePlan.ExpiredAssignments, []string{"assignment-expired"}) || !equalStringSlices(reconcilePlan.WarmPoolAssignments, []string{"warm-slot-1"}) {
		t.Fatalf("PlanReconcile = %+v, want stale worker, expired assignment, and warm slot", reconcilePlan)
	}
	reconciled, err := Reconcile(ctx, ReconcileOptions{FleetURL: server.URL, APIKey: "secret", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !equalStringSlices(reconciled.RequeuedAssignments, []string{"assignment-1"}) || !equalStringSlices(reconciled.ExpiredAssignments, []string{"assignment-expired"}) || !equalStringSlices(reconciled.WarmPoolCleanup, []string{"cleanup-1"}) {
		t.Fatalf("Reconcile = %+v, want assignment requeue, expiry, and warm cleanup", reconciled)
	}
	summary, err := GetOperationsSummary(ctx, OperationsSummaryOptions{FleetURL: server.URL, APIKey: "secret", Namespace: "team-a", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetOperationsSummary: %v", err)
	}
	if summary.Namespace != "team-a" || summary.Workers.Total != 3 || summary.Workers.Ready != 1 || summary.Assignments.Active != 1 || summary.Sandboxes.Active != 1 || summary.WarmPools.Ready != 1 || summary.Metering.Records != 2 {
		t.Fatalf("GetOperationsSummary = %+v, want team-a dashboard summary", summary)
	}
	if len(summary.Workers.Capabilities) != 2 || summary.Workers.Capabilities[0].Name != "asif" || summary.Workers.Capabilities[0].Ready != 1 {
		t.Fatalf("operations capabilities = %+v, want sorted capability coverage", summary.Workers.Capabilities)
	}
	if len(summary.Workers.Attention) != 1 || summary.Workers.Attention[0].ID != "worker-2" || !summary.Workers.Attention[0].Cordoned {
		t.Fatalf("operations attention workers = %+v, want cordoned worker-2", summary.Workers.Attention)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{
		"/v1/images/gc",
		"/v1/images/gc/runs",
		"/v1/images/gc/runs/image-gc-1",
		"/v1/policies/lifecycle",
		"/v1/policies/lifecycle/runs",
		"/v1/policies/lifecycle/runs/lifecycle-policy-1",
		"/v1/storage/budget",
		"/v1/storage/budget/runs",
		"/v1/storage/budget/runs/storage-budget-1",
		"/v1/storage/prune",
		"/v1/storage/prune/runs",
		"/v1/storage/prune/runs/storage-prune-1",
		"/v1/operations/runs",
		"/v1/reconcile/plan",
		"/v1/reconcile",
		"/v1/operations/summary",
	}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	if body := server.requests[0].body; body["namespace"] != "team-a" || body["older_than"] != "168h" || body["apply"] != true || body["dry_run"] != true || !equalAnyStringSlice(body["required_capabilities"], []string{"ram-overlay", "asif"}) {
		t.Fatalf("image gc body = %+v", body)
	}
	if query := server.requests[1].query; query.Get("namespace") != "team-a" || query.Get("older_than") != "168h" || query.Get("apply") != "true" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("image gc query = %q", query.Encode())
	}
	if body := server.requests[3].body; body["vm_name"] != "ci-runner" || body["idle_timeout"] != "30m" || body["run_budget"] != float64(100) || body["dry_run"] != true {
		t.Fatalf("lifecycle body = %+v", body)
	}
	if query := server.requests[4].query; query.Get("vm_name") != "ci-runner" || query.Get("clear") != "false" {
		t.Fatalf("lifecycle query = %q", query.Encode())
	}
	if body := server.requests[6].body; body["target"] != "750GB" || body["warn_pct"] != float64(70) || body["hard_pct"] != float64(90) || body["dry_run"] != true {
		t.Fatalf("storage budget body = %+v", body)
	}
	if query := server.requests[7].query; query.Get("target") != "750GB" || query.Get("clear") != "false" {
		t.Fatalf("storage budget query = %q", query.Encode())
	}
	if body := server.requests[9].body; body["category"] != "build-scratch" || body["older_than"] != "48h" || body["apply"] != true || body["dry_run"] != true {
		t.Fatalf("storage prune body = %+v", body)
	}
	if query := server.requests[12].query; query.Get("kind") != "storage.prune" || query.Get("target_type") != "storage" || query.Get("namespace") != "team-a" {
		t.Fatalf("controller runs query = %q", query.Encode())
	}
	if body := server.requests[14].body; len(body) != 0 {
		t.Fatalf("reconcile body = %+v, want empty body", body)
	}
	if query := server.requests[15].query; query.Get("namespace") != "team-a" {
		t.Fatalf("operations summary query = %q", query.Encode())
	}
}

func TestCloudClientAudit(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	page, err := ListAuditEvents(ctx, AuditListOptions{
		FleetURL:     server.URL,
		APIKey:       "secret",
		Namespace:    "team-a",
		Actor:        "service-account:ci",
		Action:       "assignment.create",
		TargetType:   "assignment",
		TargetID:     "assignment-1",
		WorkerID:     "worker-1",
		AssignmentID: "assignment-1",
		SandboxID:    "job-1",
		Offset:       1,
		Limit:        2,
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if page.Count != 1 || page.Offset != 1 || page.Limit != 2 || len(page.Events) != 1 || page.Events[0].ID != "audit-1" {
		t.Fatalf("ListAuditEvents = %+v, want audit-1 page", page)
	}
	if event := page.Events[0]; event.Hash == "" || event.PrevHash == "" || event.Fields["reason"] != "created" {
		t.Fatalf("audit event = %+v, want hash-chain fields", event)
	}
	verify, err := VerifyAuditLog(ctx, AuditVerifyOptions{FleetURL: server.URL, APIKey: "secret", Timeout: time.Second})
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if !verify.OK || verify.Events != 7 || verify.HeadHash != "hash-1" || len(verify.Issues) != 0 {
		t.Fatalf("VerifyAuditLog = %+v, want ok head hash", verify)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{"/v1/audit", "/v1/audit/verify"}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	query := server.requests[0].query
	if query.Get("namespace") != "team-a" || query.Get("actor") != "service-account:ci" || query.Get("action") != "assignment.create" || query.Get("target_type") != "assignment" || query.Get("target_id") != "assignment-1" || query.Get("worker_id") != "worker-1" || query.Get("assignment_id") != "assignment-1" || query.Get("sandbox_id") != "job-1" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("audit query = %q", query.Encode())
	}
}

func TestCloudClientServiceAccounts(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	page, err := ListServiceAccounts(ctx, ServiceAccountListOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ListServiceAccounts: %v", err)
	}
	if page.Count != 1 || len(page.ServiceAccounts) != 1 || page.ServiceAccounts[0].Name != "ci" {
		t.Fatalf("ListServiceAccounts = %+v, want ci account page", page)
	}
	upsert, err := UpsertServiceAccount(ctx, ServiceAccountUpsertOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Name:      "ci",
		Namespace: "team-a",
		Role:      "operator",
		Token:     "next-secret",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("UpsertServiceAccount: %v", err)
	}
	if upsert.ServiceAccount.Name != "ci" || upsert.ServiceAccount.Namespace != "team-a" || upsert.ServiceAccount.Role != "operator" {
		t.Fatalf("UpsertServiceAccount = %+v, want team-a operator", upsert)
	}
	deleted, err := DeleteServiceAccount(ctx, ServiceAccountDeleteOptions{
		FleetURL: server.URL,
		APIKey:   "secret",
		Name:     "ci",
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("DeleteServiceAccount: %v", err)
	}
	if deleted.ServiceAccount.Name != "ci" {
		t.Fatalf("DeleteServiceAccount = %+v, want ci account", deleted)
	}
	if len(server.requests) != 3 {
		t.Fatalf("requests = %+v, want 3", server.requests)
	}
	for _, req := range server.requests {
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	if paths := []string{server.requests[0].path, server.requests[1].path, server.requests[2].path}; !equalStringSlices(paths, []string{"/v1/service-accounts", "/v1/service-accounts", "/v1/service-accounts/ci"}) {
		t.Fatalf("paths = %+v", paths)
	}
	if query := server.requests[0].query; query.Get("namespace") != "team-a" {
		t.Fatalf("service account query = %q", query.Encode())
	}
	if body := server.requests[1].body; body["name"] != "ci" || body["namespace"] != "team-a" || body["role"] != "operator" || body["token"] != "next-secret" {
		t.Fatalf("service account body = %+v", body)
	}
	if _, err := UpsertServiceAccount(ctx, ServiceAccountUpsertOptions{FleetURL: "https://fleet.example", APIKey: "secret", Token: "x"}); err == nil || !strings.Contains(err.Error(), "service account name required") {
		t.Fatalf("missing service account name err = %v, want validation error", err)
	}
	if _, err := UpsertServiceAccount(ctx, ServiceAccountUpsertOptions{FleetURL: "https://fleet.example", APIKey: "secret", Name: "ci"}); err == nil || !strings.Contains(err.Error(), "service account token required") {
		t.Fatalf("missing service account token err = %v, want validation error", err)
	}
	if _, err := DeleteServiceAccount(ctx, ServiceAccountDeleteOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "service account name required") {
		t.Fatalf("missing service account delete name err = %v, want validation error", err)
	}
}

func TestCloudClientIdentityBindings(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	oidcPage, err := ListOIDCBindings(ctx, OIDCBindingListOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ListOIDCBindings: %v", err)
	}
	if oidcPage.Count != 1 || oidcPage.Bindings[0].Name != "github-main" {
		t.Fatalf("ListOIDCBindings = %+v, want github-main", oidcPage)
	}
	oidc, err := UpsertOIDCBinding(ctx, OIDCBindingUpsertOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Name:      "github-main",
		Issuer:    "https://token.actions.githubusercontent.com",
		Subject:   "repo:tmc/cove:ref:refs/heads/main",
		Audience:  "cove-fleet",
		Namespace: "team-a",
		Role:      "operator",
		JWKSURL:   "https://token.actions.githubusercontent.com/.well-known/jwks",
		Keys:      []OIDCKey{{KID: "kid-1", Alg: "RS256", PEM: "pem"}},
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("UpsertOIDCBinding: %v", err)
	}
	if oidc.Binding.Name != "github-main" || oidc.Binding.KeyIDs[0] != "kid-1" {
		t.Fatalf("UpsertOIDCBinding = %+v, want github-main", oidc)
	}
	if _, err := DeleteOIDCBinding(ctx, OIDCBindingDeleteOptions{FleetURL: server.URL, APIKey: "secret", Name: "github-main", Timeout: time.Second}); err != nil {
		t.Fatalf("DeleteOIDCBinding: %v", err)
	}
	samlPage, err := ListSAMLBindings(ctx, SAMLBindingListOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ListSAMLBindings: %v", err)
	}
	if samlPage.Count != 1 || samlPage.Bindings[0].Name != "okta" {
		t.Fatalf("ListSAMLBindings = %+v, want okta", samlPage)
	}
	saml, err := UpsertSAMLBinding(ctx, SAMLBindingUpsertOptions{
		FleetURL:       server.URL,
		APIKey:         "secret",
		Name:           "okta",
		EntityID:       "https://idp.example/saml",
		Subject:        "ci@example.com",
		SSOURL:         "https://idp.example/sso",
		Audience:       "https://fleet.example/saml/acs",
		Namespace:      "team-a",
		Role:           "operator",
		CertificatePEM: "pem",
		MetadataURL:    "https://idp.example/metadata.xml",
		MetadataXML:    "<EntityDescriptor/>",
		Timeout:        time.Second,
	})
	if err != nil {
		t.Fatalf("UpsertSAMLBinding: %v", err)
	}
	if saml.Binding.Name != "okta" || saml.Binding.CertificateSHA256 == "" {
		t.Fatalf("UpsertSAMLBinding = %+v, want okta fingerprint", saml)
	}
	if _, err := RefreshSAMLBinding(ctx, SAMLBindingNameOptions{FleetURL: server.URL, APIKey: "secret", Name: "okta", Timeout: time.Second}); err != nil {
		t.Fatalf("RefreshSAMLBinding: %v", err)
	}
	metadata, err := GetSAMLMetadata(ctx, SAMLBindingNameOptions{FleetURL: server.URL, APIKey: "secret", Name: "okta", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetSAMLMetadata: %v", err)
	}
	if !strings.Contains(string(metadata), "EntityDescriptor") || !strings.Contains(string(metadata), "https://fleet.example/saml/acs") {
		t.Fatalf("GetSAMLMetadata = %q, want SP metadata", metadata)
	}
	login, err := SAMLBindingLogin(ctx, SAMLBindingLoginOptions{FleetURL: server.URL, APIKey: "secret", Name: "okta", RelayState: "cli", Timeout: time.Second})
	if err != nil {
		t.Fatalf("SAMLBindingLogin: %v", err)
	}
	if login.Binding.Name != "okta" || login.RelayState != "cli" || login.RedirectURL == "" {
		t.Fatalf("SAMLBindingLogin = %+v, want redirect", login)
	}
	session, err := CreateSAMLSession(ctx, SAMLSessionOptions{FleetURL: server.URL, APIKey: "secret", SAMLResponse: "response", RelayState: "cli", TTL: "1h", Timeout: time.Second})
	if err != nil {
		t.Fatalf("CreateSAMLSession: %v", err)
	}
	if session.Token != "saml-session-token" || session.Binding.Name != "okta" {
		t.Fatalf("CreateSAMLSession = %+v, want token", session)
	}
	if _, err := DeleteSAMLBinding(ctx, SAMLBindingNameOptions{FleetURL: server.URL, APIKey: "secret", Name: "okta", Timeout: time.Second}); err != nil {
		t.Fatalf("DeleteSAMLBinding: %v", err)
	}
	wantPaths := []string{
		"/v1/oidc-bindings",
		"/v1/oidc-bindings",
		"/v1/oidc-bindings/github-main",
		"/v1/saml-bindings",
		"/v1/saml-bindings",
		"/v1/saml-bindings/okta/refresh",
		"/v1/saml-bindings/okta/metadata",
		"/v1/saml-bindings/okta/login",
		"/v1/saml/acs",
		"/v1/saml-bindings/okta",
	}
	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	if query := server.requests[0].query; query.Get("namespace") != "team-a" {
		t.Fatalf("oidc list query = %q", query.Encode())
	}
	if body := server.requests[1].body; body["issuer"] != "https://token.actions.githubusercontent.com" || body["namespace"] != "team-a" || body["jwks_url"] == "" {
		t.Fatalf("oidc body = %+v", body)
	}
	if query := server.requests[7].query; query.Get("relay_state") != "cli" {
		t.Fatalf("saml login query = %q", query.Encode())
	}
	if body := server.requests[8].body; body["saml_response"] != "response" || body["relay_state"] != "cli" || body["ttl"] != "1h" {
		t.Fatalf("saml session body = %+v", body)
	}
	if _, err := UpsertOIDCBinding(ctx, OIDCBindingUpsertOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "oidc binding name required") {
		t.Fatalf("missing oidc binding name err = %v, want validation error", err)
	}
	if _, err := DeleteOIDCBinding(ctx, OIDCBindingDeleteOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "oidc binding name required") {
		t.Fatalf("missing oidc binding delete name err = %v, want validation error", err)
	}
	if _, err := UpsertSAMLBinding(ctx, SAMLBindingUpsertOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "saml binding name required") {
		t.Fatalf("missing saml binding name err = %v, want validation error", err)
	}
	if _, err := SAMLBindingLogin(ctx, SAMLBindingLoginOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "saml binding name required") {
		t.Fatalf("missing saml login name err = %v, want validation error", err)
	}
	if _, err := GetSAMLMetadata(ctx, SAMLBindingNameOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "saml binding name required") {
		t.Fatalf("missing saml metadata name err = %v, want validation error", err)
	}
	if _, err := CreateSAMLSession(ctx, SAMLSessionOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "saml response or assertion required") {
		t.Fatalf("missing saml response err = %v, want validation error", err)
	}
}

func TestCloudClientScopedObservability(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	sandboxes, err := ListWorkerSandboxes(ctx, WorkerSandboxListOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		ID:        "worker-1",
		Namespace: "team-a",
		Status:    "ready",
		ImageRef:  "base:v1",
		Offset:    1,
		Limit:     2,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ListWorkerSandboxes: %v", err)
	}
	if sandboxes.Count != 1 || sandboxes.Sandboxes[0].WorkerID != "worker-1" {
		t.Fatalf("ListWorkerSandboxes = %+v, want worker-1 sandbox", sandboxes)
	}
	workerEvents, err := ListWorkerEvents(ctx, WorkerEventListOptions{
		FleetURL:   server.URL,
		APIKey:     "secret",
		ID:         "worker-1",
		Actor:      "service-account:ci",
		Action:     "assignment.create",
		TargetType: "assignment",
		TargetID:   "assignment-1",
		SandboxID:  "job-1",
		Offset:     1,
		Limit:      2,
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("ListWorkerEvents: %v", err)
	}
	if workerEvents.Count != 1 || workerEvents.Events[0].WorkerID != "worker-1" {
		t.Fatalf("ListWorkerEvents = %+v, want worker-1 event", workerEvents)
	}
	workerReports, err := ListWorkerReports(ctx, WorkerReportListOptions{
		FleetURL:     server.URL,
		APIKey:       "secret",
		ID:           "worker-1",
		AssignmentID: "assignment-1",
		Status:       "complete",
		Offset:       1,
		Limit:        2,
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatalf("ListWorkerReports: %v", err)
	}
	if workerReports.Count != 1 || workerReports.Reports[0].Report.Stdout != "out" {
		t.Fatalf("ListWorkerReports = %+v, want report stdout", workerReports)
	}
	workerMetering, err := GetWorkerMetering(ctx, WorkerMeteringOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		ID:        "worker-1",
		Namespace: "team-a",
		SandboxID: "job-1",
		Status:    "ready",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("GetWorkerMetering: %v", err)
	}
	if workerMetering.Summary.WorkerID != "worker-1" || workerMetering.Records[0].Resources.VMs != 1 {
		t.Fatalf("GetWorkerMetering = %+v, want worker-1 resource metering", workerMetering)
	}
	sandboxMetering, err := ListSandboxMetering(ctx, SandboxMeteringOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		SandboxID: "job-1",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ListSandboxMetering: %v", err)
	}
	if sandboxMetering.Summary.SandboxID != "job-1" || sandboxMetering.Records[0].SandboxID != "job-1" {
		t.Fatalf("ListSandboxMetering = %+v, want job-1 metering", sandboxMetering)
	}
	assignmentEvents, err := ListAssignmentEvents(ctx, AssignmentEventListOptions{
		FleetURL:   server.URL,
		APIKey:     "secret",
		ID:         "assignment-1",
		Actor:      "service-account:ci",
		Action:     "assignment.create",
		TargetType: "assignment",
		TargetID:   "assignment-1",
		WorkerID:   "worker-1",
		SandboxID:  "job-1",
		Offset:     1,
		Limit:      2,
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("ListAssignmentEvents: %v", err)
	}
	if assignmentEvents.Count != 1 || assignmentEvents.Events[0].AssignmentID != "assignment-1" {
		t.Fatalf("ListAssignmentEvents = %+v, want assignment-1 event", assignmentEvents)
	}
	assignmentReports, err := ListAssignmentReports(ctx, AssignmentReportListOptions{
		FleetURL: server.URL,
		APIKey:   "secret",
		ID:       "assignment-1",
		WorkerID: "worker-1",
		Status:   "complete",
		Offset:   1,
		Limit:    2,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("ListAssignmentReports: %v", err)
	}
	if assignmentReports.Count != 1 || assignmentReports.Reports[0].AssignmentID != "assignment-1" {
		t.Fatalf("ListAssignmentReports = %+v, want assignment-1 report", assignmentReports)
	}
	assignmentMetering, err := GetAssignmentMetering(ctx, AssignmentMeteringOptions{
		FleetURL: server.URL,
		APIKey:   "secret",
		ID:       "assignment-1",
		Status:   "ready",
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("GetAssignmentMetering: %v", err)
	}
	if assignmentMetering.Summary.AssignmentID != "assignment-1" || assignmentMetering.Records[0].WorkerID != "worker-1" {
		t.Fatalf("GetAssignmentMetering = %+v, want assignment-1 metering", assignmentMetering)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{
		"/v1/workers/worker-1/sandboxes",
		"/v1/workers/worker-1/events",
		"/v1/workers/worker-1/reports",
		"/v1/workers/worker-1/metering",
		"/v1/metering/sandboxes",
		"/v1/assignments/assignment-1/events",
		"/v1/assignments/assignment-1/reports",
		"/v1/assignments/assignment-1/metering",
	}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	if query := server.requests[0].query; query.Get("namespace") != "team-a" || query.Get("status") != "ready" || query.Get("image_ref") != "base:v1" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("worker sandboxes query = %q", query.Encode())
	}
	if query := server.requests[1].query; query.Get("actor") != "service-account:ci" || query.Get("action") != "assignment.create" || query.Get("target_type") != "assignment" || query.Get("target_id") != "assignment-1" || query.Get("sandbox_id") != "job-1" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("worker events query = %q", query.Encode())
	}
	if query := server.requests[2].query; query.Get("assignment_id") != "assignment-1" || query.Get("status") != "complete" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("worker reports query = %q", query.Encode())
	}
	if query := server.requests[3].query; query.Get("namespace") != "team-a" || query.Get("sandbox_id") != "job-1" || query.Get("status") != "ready" {
		t.Fatalf("worker metering query = %q", query.Encode())
	}
	if query := server.requests[4].query; query.Get("namespace") != "team-a" || query.Get("sandbox_id") != "job-1" {
		t.Fatalf("sandbox metering query = %q", query.Encode())
	}
	if query := server.requests[5].query; query.Get("actor") != "service-account:ci" || query.Get("worker_id") != "worker-1" || query.Get("sandbox_id") != "job-1" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("assignment events query = %q", query.Encode())
	}
	if query := server.requests[6].query; query.Get("worker_id") != "worker-1" || query.Get("status") != "complete" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("assignment reports query = %q", query.Encode())
	}
	if query := server.requests[7].query; query.Get("status") != "ready" {
		t.Fatalf("assignment metering query = %q", query.Encode())
	}
}

func TestCloudClientInventory(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()

	workers, err := ListWorkers(ctx, WorkerListOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Status:               "ready",
		Host:                 "mini-1",
		Version:              "dev",
		ImageRef:             "base:v1",
		SourceManifestDigest: "sha256:base",
		Labels:               map[string]string{"zone": "desk", "role": "runner"},
		Capabilities:         []string{"ram-overlay", "asif", "ram-overlay", ""},
		Offset:               1,
		Limit:                2,
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if workers.Count != 1 || workers.Offset != 1 || workers.Limit != 2 || len(workers.Workers) != 1 || workers.Workers[0].ID != "worker-1" {
		t.Fatalf("ListWorkers = %+v, want worker-1 page", workers)
	}
	if !workers.Workers[0].Cordoned || workers.Workers[0].Labels["zone"] != "desk" || !equalStringSlices(workers.Workers[0].Capabilities, []string{"ram-overlay", "asif"}) || workers.Workers[0].ImageDetails[0].SourceManifestDigest != "sha256:base" {
		t.Fatalf("ListWorkers worker = %+v, want decoded inventory details", workers.Workers[0])
	}
	worker, err := GetWorker(ctx, WorkerGetOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if worker.ID != "worker-1" || worker.Capacity.MaxVMs != 4 {
		t.Fatalf("GetWorker = %+v, want worker-1", worker)
	}

	created, err := CreateAssignment(ctx, AssignmentCreateOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		ID:                   "assignment-created",
		Policy:               "bin-pack",
		ImageRef:             "base:v1",
		ManifestBundle:       "manifests",
		ImageManifestDigest:  "sha256:base",
		ImageDigestRef:       "ghcr.io/me/base@sha256:base",
		ImagePlatform:        "darwin/arm64",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif", ""},
		AntiAffinityKey:      "ci/buildkite",
		Resources:            Capacity{VMs: 1, CPUs: 4},
		Priority:             8,
		QueueTTL:             2 * time.Minute,
		RunTimeout:           5 * time.Minute,
		MaxAttempts:          4,
		RetryDelay:           30 * time.Second,
		Verb:                 "cove",
		Args:                 []string{"run", "-fork-from", "base:v1", "-ephemeral"},
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	if created.ID != "assignment-created" || created.WorkerID != "worker-1" || created.Policy != "bin-pack" || created.Resources.CPUs != 4 || created.Priority != 8 || created.QueueExpires.IsZero() || created.MaxAttempts != 4 || created.RetryDelay != "30s" {
		t.Fatalf("CreateAssignment = %+v, want scheduled assignment", created)
	}

	assignments, err := ListAssignments(ctx, AssignmentListOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		Status:    "running",
		WorkerID:  "worker-1",
		LeasedTo:  "worker-1",
		Verb:      "cove",
		ImageRef:  "base:v1",
		SandboxID: "job-1",
		WarmPool:  "runner",
		Offset:    1,
		Limit:     2,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if assignments.Count != 1 || assignments.Offset != 1 || assignments.Limit != 2 || len(assignments.Assignments) != 1 || assignments.Assignments[0].ID != "assignment-1" {
		t.Fatalf("ListAssignments = %+v, want assignment-1 page", assignments)
	}
	assignment, err := GetAssignment(ctx, AssignmentGetOptions{FleetURL: server.URL, APIKey: "secret", ID: "assignment-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetAssignment: %v", err)
	}
	if assignment.ID != "assignment-1" || assignment.SandboxID != "job-1" || assignment.WarmPool != "runner" {
		t.Fatalf("GetAssignment = %+v, want assignment-1", assignment)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{
		"/v1/workers",
		"/v1/workers/worker-1",
		"/v1/assignments",
		"/v1/assignments",
		"/v1/assignments/assignment-1",
	}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	if query := server.requests[0].query; query.Get("status") != "ready" || query.Get("host") != "mini-1" || query.Get("version") != "dev" || query.Get("image_ref") != "base:v1" || query.Get("source_manifest_digest") != "sha256:base" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("worker query = %q", query.Encode())
	}
	if labels := server.requests[0].query["label"]; !equalStringSlices(labels, []string{"role=runner", "zone=desk"}) {
		t.Fatalf("worker label query = %+v, want sorted labels", labels)
	}
	if capabilities := server.requests[0].query["capability"]; !equalStringSlices(capabilities, []string{"ram-overlay", "asif"}) {
		t.Fatalf("worker capability query = %+v, want deduped capabilities", capabilities)
	}
	createBody := server.requests[2].body
	if createBody["id"] != "assignment-created" || createBody["namespace"] != "team-a" || createBody["policy"] != "bin-pack" || createBody["image_ref"] != "base:v1" || createBody["manifest_bundle"] != "manifests" || createBody["image_manifest_digest"] != "sha256:base" || createBody["image_digest_ref"] != "ghcr.io/me/base@sha256:base" || createBody["image_platform"] != "darwin/arm64" || createBody["anti_affinity_key"] != "ci/buildkite" || createBody["verb"] != "cove" {
		t.Fatalf("create assignment body = %+v, want placement identity", createBody)
	}
	if !equalAnyStringSlice(createBody["required_capabilities"], []string{"ram-overlay", "asif"}) {
		t.Fatalf("create assignment capabilities = %+v, want ram-overlay/asif", createBody["required_capabilities"])
	}
	resources, ok := createBody["resources"].(map[string]any)
	if !ok || resources["vms"] != float64(1) || resources["cpus"] != float64(4) {
		t.Fatalf("create assignment resources = %+v, want vms/cpus", createBody["resources"])
	}
	if createBody["priority"] != float64(8) {
		t.Fatalf("create assignment priority = %+v, want 8", createBody["priority"])
	}
	if createBody["queue_ttl"] != "120s" {
		t.Fatalf("create assignment queue ttl = %+v, want 120s", createBody["queue_ttl"])
	}
	if createBody["run_timeout"] != "300s" {
		t.Fatalf("create assignment run timeout = %+v, want 300s", createBody["run_timeout"])
	}
	if createBody["max_attempts"] != float64(4) {
		t.Fatalf("create assignment max attempts = %+v, want 4", createBody["max_attempts"])
	}
	if createBody["retry_delay"] != "30s" {
		t.Fatalf("create assignment retry delay = %+v, want 30s", createBody["retry_delay"])
	}
	if !equalAnyStringSlice(createBody["args"], []string{"run", "-fork-from", "base:v1", "-ephemeral"}) {
		t.Fatalf("create assignment args = %+v, want run args", createBody["args"])
	}
	if query := server.requests[3].query; query.Get("namespace") != "team-a" || query.Get("status") != "running" || query.Get("worker_id") != "worker-1" || query.Get("leased_to") != "worker-1" || query.Get("verb") != "cove" || query.Get("image_ref") != "base:v1" || query.Get("sandbox_id") != "job-1" || query.Get("warm_pool") != "runner" || query.Get("offset") != "1" || query.Get("limit") != "2" {
		t.Fatalf("assignment query = %q", query.Encode())
	}
}

func TestCloudClientInventoryValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := ListWorkers(ctx, WorkerListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("ListWorkers negative limit err = %v, want validation error", err)
	}
	if _, err := GetWorker(ctx, WorkerGetOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("GetWorker missing id err = %v, want validation error", err)
	}
	if _, err := ListAssignments(ctx, AssignmentListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("ListAssignments negative offset err = %v, want validation error", err)
	}
	if _, err := CreateAssignment(ctx, AssignmentCreateOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "verb required") {
		t.Fatalf("CreateAssignment missing verb err = %v, want validation error", err)
	}
	if _, err := CreateAssignment(ctx, AssignmentCreateOptions{FleetURL: "https://fleet.example", APIKey: "secret", Verb: "noop", Priority: -1}); err == nil || !strings.Contains(err.Error(), "priority must be non-negative") {
		t.Fatalf("CreateAssignment negative priority err = %v, want validation error", err)
	}
	if _, err := CreateAssignment(ctx, AssignmentCreateOptions{FleetURL: "https://fleet.example", APIKey: "secret", Verb: "noop", QueueTTL: -time.Second}); err == nil || !strings.Contains(err.Error(), "queue ttl must not be negative") {
		t.Fatalf("CreateAssignment negative queue ttl err = %v, want validation error", err)
	}
	if _, err := CreateAssignment(ctx, AssignmentCreateOptions{FleetURL: "https://fleet.example", APIKey: "secret", Verb: "noop", RunTimeout: -time.Second}); err == nil || !strings.Contains(err.Error(), "run timeout must not be negative") {
		t.Fatalf("CreateAssignment negative run timeout err = %v, want validation error", err)
	}
	if _, err := CreateAssignment(ctx, AssignmentCreateOptions{FleetURL: "https://fleet.example", APIKey: "secret", Verb: "noop", MaxAttempts: -1}); err == nil || !strings.Contains(err.Error(), "max attempts must be non-negative") {
		t.Fatalf("CreateAssignment negative max attempts err = %v, want validation error", err)
	}
	if _, err := CreateAssignment(ctx, AssignmentCreateOptions{FleetURL: "https://fleet.example", APIKey: "secret", Verb: "noop", RetryDelay: -time.Second}); err == nil || !strings.Contains(err.Error(), "retry delay must not be negative") {
		t.Fatalf("CreateAssignment negative retry delay err = %v, want validation error", err)
	}
	if _, err := GetAssignment(ctx, AssignmentGetOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("GetAssignment missing id err = %v, want validation error", err)
	}
}

func TestCloudClientAuditValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := ListAuditEvents(ctx, AuditListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("ListAuditEvents negative limit err = %v, want validation error", err)
	}
	if _, err := ListAuditEvents(ctx, AuditListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("ListAuditEvents negative offset err = %v, want validation error", err)
	}
}

func TestCloudClientScopedObservabilityValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := ListWorkerEvents(ctx, WorkerEventListOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "worker id required") {
		t.Fatalf("ListWorkerEvents missing id err = %v, want validation error", err)
	}
	if _, err := ListWorkerReports(ctx, WorkerReportListOptions{FleetURL: "https://fleet.example", APIKey: "secret", ID: "worker-1", Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("ListWorkerReports negative limit err = %v, want validation error", err)
	}
	if _, err := ListWorkerSandboxes(ctx, WorkerSandboxListOptions{FleetURL: "https://fleet.example", APIKey: "secret", ID: "worker-1", Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("ListWorkerSandboxes negative offset err = %v, want validation error", err)
	}
	if _, err := GetWorkerMetering(ctx, WorkerMeteringOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "worker id required") {
		t.Fatalf("GetWorkerMetering missing id err = %v, want validation error", err)
	}
	if _, err := ListAssignmentEvents(ctx, AssignmentEventListOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "assignment id required") {
		t.Fatalf("ListAssignmentEvents missing id err = %v, want validation error", err)
	}
	if _, err := ListAssignmentReports(ctx, AssignmentReportListOptions{FleetURL: "https://fleet.example", APIKey: "secret", ID: "assignment-1", Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("ListAssignmentReports negative limit err = %v, want validation error", err)
	}
	if _, err := GetAssignmentMetering(ctx, AssignmentMeteringOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "assignment id required") {
		t.Fatalf("GetAssignmentMetering missing id err = %v, want validation error", err)
	}
}

func TestCloudClientAssignmentControls(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()

	canceled, err := CancelAssignment(ctx, AssignmentCancelOptions{
		FleetURL: server.URL,
		APIKey:   "secret",
		ID:       "assignment-1",
		Reason:   "bad input",
		Force:    true,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("CancelAssignment: %v", err)
	}
	if !canceled.Canceled || !canceled.Force || canceled.Reason != "bad input" || canceled.PreviousStatus != "running" || canceled.Assignment.Status != "canceled" {
		t.Fatalf("CancelAssignment = %+v, want canceled running assignment", canceled)
	}
	retried, err := RetryAssignment(ctx, AssignmentRetryOptions{
		FleetURL: server.URL,
		APIKey:   "secret",
		ID:       "assignment-1",
		Reason:   "transient",
		WorkerID: "worker-2",
		Replan:   true,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("RetryAssignment: %v", err)
	}
	if retried.Reason != "transient" || retried.PreviousStatus != "failed" || retried.PreviousWorkerID != "worker-1" || !retried.Replanned || retried.Assignment.Status != "pending" || retried.Assignment.WorkerID != "worker-2" {
		t.Fatalf("RetryAssignment = %+v, want replanned pending assignment", retried)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{
		"/v1/assignments/assignment-1/cancel",
		"/v1/assignments/assignment-1/retry",
	}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	if body := server.requests[0].body; body["reason"] != "bad input" || body["force"] != true {
		t.Fatalf("cancel body = %+v, want reason and force", body)
	}
	if body := server.requests[1].body; body["reason"] != "transient" || body["worker_id"] != "worker-2" || body["replan"] != true {
		t.Fatalf("retry body = %+v, want reason, worker, and replan", body)
	}
}

func TestCloudClientAssignmentControlValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := CancelAssignment(ctx, AssignmentCancelOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "assignment id required") {
		t.Fatalf("CancelAssignment missing id err = %v, want validation error", err)
	}
	if _, err := RetryAssignment(ctx, AssignmentRetryOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "assignment id required") {
		t.Fatalf("RetryAssignment missing id err = %v, want validation error", err)
	}
}

func TestCloudClientWorkerLifecycle(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()

	cordoned, err := CordonWorker(ctx, WorkerLifecycleOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Reason: "maintenance", Timeout: time.Second})
	if err != nil {
		t.Fatalf("CordonWorker: %v", err)
	}
	if !cordoned.Cordoned || cordoned.CordonReason != "maintenance" || cordoned.Status != "cordoned" {
		t.Fatalf("CordonWorker = %+v, want cordoned worker", cordoned)
	}
	uncordoned, err := UncordonWorker(ctx, WorkerLifecycleOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("UncordonWorker: %v", err)
	}
	if uncordoned.Cordoned || uncordoned.Status != "ready" {
		t.Fatalf("UncordonWorker = %+v, want ready worker", uncordoned)
	}
	quarantined, err := QuarantineWorker(ctx, WorkerLifecycleOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Reason: "bad disk", Timeout: time.Second})
	if err != nil {
		t.Fatalf("QuarantineWorker: %v", err)
	}
	if !quarantined.Quarantined || quarantined.QuarantineReason != "bad disk" || quarantined.Status != "quarantined" {
		t.Fatalf("QuarantineWorker = %+v, want quarantined worker", quarantined)
	}
	unquarantined, err := UnquarantineWorker(ctx, WorkerLifecycleOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("UnquarantineWorker: %v", err)
	}
	if unquarantined.Quarantined || unquarantined.Status != "ready" {
		t.Fatalf("UnquarantineWorker = %+v, want ready worker", unquarantined)
	}
	plan, err := EvacuateWorker(ctx, WorkerEvacuationOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Reason: "maintenance", Timeout: time.Second})
	if err != nil {
		t.Fatalf("EvacuateWorker plan: %v", err)
	}
	if plan.Apply || plan.Applied || len(plan.Assignments) != 2 || plan.Assignments[0].Action != "requeue" || plan.Assignments[0].TargetWorkerID != "worker-2" || len(plan.Blocked) != 1 {
		t.Fatalf("EvacuateWorker plan = %+v, want dry-run requeue and blocker", plan)
	}
	applied, err := EvacuateWorker(ctx, WorkerEvacuationOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Reason: "maintenance", Apply: true, Force: true, Timeout: time.Second})
	if err != nil {
		t.Fatalf("EvacuateWorker apply: %v", err)
	}
	if !applied.Apply || !applied.Applied || !applied.Force || len(applied.Requeued) != 1 || applied.Requeued[0].WorkerID != "worker-2" || len(applied.Canceled) != 1 {
		t.Fatalf("EvacuateWorker apply = %+v, want applied requeue and cancellation", applied)
	}
	drained, err := DrainWorker(ctx, WorkerLifecycleOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Reason: "maintenance", Timeout: time.Second})
	if err != nil {
		t.Fatalf("DrainWorker: %v", err)
	}
	if !drained.Worker.Cordoned || len(drained.Sandboxes) != 1 || drained.Sandboxes[0].ID != "job-1" || len(drained.Skipped) != 1 {
		t.Fatalf("DrainWorker = %+v, want stopped sandbox and skipped terminal sandbox", drained)
	}
	decommissioned, err := DecommissionWorker(ctx, WorkerLifecycleOptions{FleetURL: server.URL, APIKey: "secret", ID: "worker-1", Reason: "retire", Force: true, Timeout: time.Second})
	if err != nil {
		t.Fatalf("DecommissionWorker: %v", err)
	}
	if !decommissioned.Removed || !decommissioned.Force || decommissioned.Reason != "retire" || !equalStringSlices(decommissioned.Canceled, []string{"assignment-1"}) {
		t.Fatalf("DecommissionWorker = %+v, want forced removal", decommissioned)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{
		"/v1/workers/worker-1/cordon",
		"/v1/workers/worker-1/uncordon",
		"/v1/workers/worker-1/quarantine",
		"/v1/workers/worker-1/unquarantine",
		"/v1/workers/worker-1/evacuate",
		"/v1/workers/worker-1/evacuate",
		"/v1/workers/worker-1/drain",
		"/v1/workers/worker-1/decommission",
	}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	if body := server.requests[0].body; body["reason"] != "maintenance" {
		t.Fatalf("cordon body = %+v, want reason", body)
	}
	if body := server.requests[1].body; len(body) != 0 {
		t.Fatalf("uncordon body = %+v, want empty body", body)
	}
	if body := server.requests[4].body; body["reason"] != "maintenance" || body["apply"] != nil || body["force"] != nil {
		t.Fatalf("evacuate plan body = %+v, want dry-run reason only", body)
	}
	if body := server.requests[5].body; body["apply"] != true || body["force"] != true {
		t.Fatalf("evacuate apply body = %+v, want apply force", body)
	}
	if body := server.requests[7].body; body["reason"] != "retire" || body["force"] != true {
		t.Fatalf("decommission body = %+v, want forced retire", body)
	}
}

func TestCloudClientWorkerLifecycleValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := CordonWorker(ctx, WorkerLifecycleOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "worker id required") {
		t.Fatalf("CordonWorker missing id err = %v, want validation error", err)
	}
	if _, err := EvacuateWorker(ctx, WorkerEvacuationOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "worker id required") {
		t.Fatalf("EvacuateWorker missing id err = %v, want validation error", err)
	}
	if _, err := DecommissionWorker(ctx, WorkerLifecycleOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "worker id required") {
		t.Fatalf("DecommissionWorker missing id err = %v, want validation error", err)
	}
}

func TestCloudClientMaintenanceValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := ListImageGCRuns(ctx, ImageGCListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("ListImageGCRuns negative limit err = %v, want validation error", err)
	}
	if _, err := GetImageGCRun(ctx, ImageGCGetOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "id required") {
		t.Fatalf("GetImageGCRun missing id err = %v, want validation error", err)
	}
	if _, err := PushLifecyclePolicy(ctx, LifecyclePolicyOptions{FleetURL: "https://fleet.example", APIKey: "secret", IdleTimeout: "1m"}); err == nil || !strings.Contains(err.Error(), "vm name required") {
		t.Fatalf("PushLifecyclePolicy missing vm err = %v, want validation error", err)
	}
	if _, err := PushLifecyclePolicy(ctx, LifecyclePolicyOptions{FleetURL: "https://fleet.example", APIKey: "secret", VMName: "vm"}); err == nil || !strings.Contains(err.Error(), "threshold required") {
		t.Fatalf("PushLifecyclePolicy missing threshold err = %v, want validation error", err)
	}
	if _, err := PushStorageBudget(ctx, StorageBudgetOptions{FleetURL: "https://fleet.example", APIKey: "secret"}); err == nil || !strings.Contains(err.Error(), "target required") {
		t.Fatalf("PushStorageBudget missing target err = %v, want validation error", err)
	}
	if _, err := PushStorageBudget(ctx, StorageBudgetOptions{FleetURL: "https://fleet.example", APIKey: "secret", Clear: true, Target: "1GB"}); err == nil || !strings.Contains(err.Error(), "clear cannot include thresholds") {
		t.Fatalf("PushStorageBudget clear thresholds err = %v, want validation error", err)
	}
	if _, err := ListControllerRuns(ctx, ControllerRunListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Offset: -1}); err == nil || !strings.Contains(err.Error(), "offset must be non-negative") {
		t.Fatalf("ListControllerRuns negative offset err = %v, want validation error", err)
	}
}

func TestCloudClientWarmPools(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	result, err := EnsureWarmPool(ctx, WarmPoolOptions{
		FleetURL:             server.URL,
		APIKey:               "secret",
		Namespace:            "team-a",
		Name:                 "runner",
		ImageRef:             "base:v1",
		ManifestBundle:       "manifests",
		ImagePlatform:        "darwin/arm64",
		Size:                 2,
		Policy:               "bin-pack",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif", ""},
		Resources:            Capacity{VMs: 1, CPUs: 4},
		Args:                 []string{"-memory", "8G"},
		Timeout:              time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pool.Name != "runner" || result.Pool.Ready != 1 || len(result.Created) != 1 {
		t.Fatalf("EnsureWarmPool = %+v, want runner with created slot", result)
	}
	pools, err := ListWarmPools(ctx, WarmPoolListOptions{FleetURL: server.URL, APIKey: "secret", Namespace: "team-a", Timeout: time.Second})
	if err != nil {
		t.Fatalf("ListWarmPools: %v", err)
	}
	if len(pools) != 1 || pools[0].Name != "runner" {
		t.Fatalf("ListWarmPools = %+v, want runner", pools)
	}
	status, err := GetWarmPool(ctx, WarmPoolGetOptions{FleetURL: server.URL, APIKey: "secret", Name: "runner", Timeout: time.Second})
	if err != nil {
		t.Fatalf("GetWarmPool: %v", err)
	}
	if status.Name != "runner" || len(status.Assignments) != 1 || status.Assignments[0].Status != "ready" {
		t.Fatalf("GetWarmPool = %+v, want ready runner slot", status)
	}
	claim, err := ClaimWarmPool(ctx, WarmPoolClaimOptions{
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		Name:      "runner",
		Command:   []string{"/bin/sh", "-lc", "make test"},
		Env:       map[string]string{"CI": "1"},
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("ClaimWarmPool: %v", err)
	}
	if claim.Pool != "runner" || claim.Slot.ID != "warm-slot-1" || claim.Assignment.WarmPoolSlot != "warm-slot-1" {
		t.Fatalf("ClaimWarmPool = %+v, want claimed warm-slot-1", claim)
	}
	events, err := WarmPoolEvents(ctx, WarmPoolEventListOptions{
		FleetURL:     server.URL,
		APIKey:       "secret",
		Name:         "runner",
		Actor:        "service-account:ci",
		Action:       "warm_pool.claim",
		WorkerID:     "worker-1",
		AssignmentID: "claim-1",
		Offset:       1,
		Limit:        2,
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatalf("WarmPoolEvents: %v", err)
	}
	if events.Count != 1 || len(events.Events) != 1 || events.Events[0].Action != "warm_pool.claim" {
		t.Fatalf("WarmPoolEvents = %+v, want warm_pool.claim", events)
	}
	deleted, err := DeleteWarmPool(ctx, WarmPoolGetOptions{FleetURL: server.URL, APIKey: "secret", Name: "runner", Timeout: time.Second})
	if err != nil {
		t.Fatalf("DeleteWarmPool: %v", err)
	}
	if deleted.Pool != "runner" || len(deleted.Cleanup) != 1 {
		t.Fatalf("DeleteWarmPool = %+v, want runner cleanup", deleted)
	}

	paths := make([]string, 0, len(server.requests))
	for _, req := range server.requests {
		paths = append(paths, req.path)
		if req.authorization != "Bearer secret" {
			t.Fatalf("authorization for %s = %q, want bearer token", req.path, req.authorization)
		}
	}
	wantPaths := []string{
		"/v1/warm-pools",
		"/v1/warm-pools",
		"/v1/warm-pools/runner",
		"/v1/warm-pools/claim",
		"/v1/warm-pools/runner/events",
		"/v1/warm-pools/runner",
	}
	if !equalStringSlices(paths, wantPaths) {
		t.Fatalf("paths = %+v, want %+v", paths, wantPaths)
	}
	ensure := server.requests[0].body
	if ensure["namespace"] != "team-a" || ensure["name"] != "runner" || ensure["image_ref"] != "base:v1" || ensure["size"] != float64(2) || ensure["policy"] != "bin-pack" {
		t.Fatalf("warm pool body = %+v, want identity and size", ensure)
	}
	if ensure["manifest_bundle"] != "manifests" || ensure["image_platform"] != "darwin/arm64" {
		t.Fatalf("warm pool image identity = %+v, want manifest bundle fields", ensure)
	}
	labels, ok := ensure["required_labels"].(map[string]any)
	if !ok || labels["zone"] != "desk" {
		t.Fatalf("warm pool labels = %+v, want zone=desk", ensure["required_labels"])
	}
	if !equalAnyStringSlice(ensure["required_capabilities"], []string{"ram-overlay", "asif"}) {
		t.Fatalf("warm pool capabilities = %+v, want ram-overlay/asif", ensure["required_capabilities"])
	}
	resources, ok := ensure["resources"].(map[string]any)
	if !ok || resources["vms"] != float64(1) || resources["cpus"] != float64(4) {
		t.Fatalf("warm pool resources = %+v, want vms/cpus", ensure["resources"])
	}
	if !equalAnyStringSlice(ensure["args"], []string{"-memory", "8G"}) {
		t.Fatalf("warm pool args = %+v, want memory arg", ensure["args"])
	}
	if server.requests[1].query.Get("namespace") != "team-a" {
		t.Fatalf("warm pool list query = %q, want namespace", server.requests[1].query.Encode())
	}
	claimBody := server.requests[3].body
	if claimBody["namespace"] != "team-a" || claimBody["name"] != "runner" {
		t.Fatalf("claim body = %+v, want namespace/name", claimBody)
	}
	if !equalAnyStringSlice(claimBody["command"], []string{"/bin/sh", "-lc", "make test"}) {
		t.Fatalf("claim command = %+v, want shell command", claimBody["command"])
	}
	env, ok := claimBody["env"].(map[string]any)
	if !ok || env["CI"] != "1" {
		t.Fatalf("claim env = %+v, want CI=1", claimBody["env"])
	}
	eventQuery := server.requests[4].query
	if eventQuery.Get("actor") != "service-account:ci" || eventQuery.Get("action") != "warm_pool.claim" || eventQuery.Get("worker_id") != "worker-1" || eventQuery.Get("assignment_id") != "claim-1" || eventQuery.Get("offset") != "1" || eventQuery.Get("limit") != "2" {
		t.Fatalf("warm pool event query = %q", eventQuery.Encode())
	}
}

func TestCloudClientWarmPoolValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := EnsureWarmPool(ctx, WarmPoolOptions{FleetURL: "https://fleet.example", APIKey: "secret", Name: "runner", Size: 1}); err == nil || !strings.Contains(err.Error(), "image ref required") {
		t.Fatalf("EnsureWarmPool missing image err = %v, want validation error", err)
	}
	if _, err := EnsureWarmPool(ctx, WarmPoolOptions{FleetURL: "https://fleet.example", APIKey: "secret", Name: "runner", ImageRef: "base:v1", Size: -1}); err == nil || !strings.Contains(err.Error(), "size must be non-negative") {
		t.Fatalf("EnsureWarmPool negative size err = %v, want validation error", err)
	}
	if _, err := ClaimWarmPool(ctx, WarmPoolClaimOptions{FleetURL: "https://fleet.example", APIKey: "secret", Name: "runner"}); err == nil || !strings.Contains(err.Error(), "claim command required") {
		t.Fatalf("ClaimWarmPool missing command err = %v, want validation error", err)
	}
	if _, err := WarmPoolEvents(ctx, WarmPoolEventListOptions{FleetURL: "https://fleet.example", APIKey: "secret", Name: "runner", Limit: -1}); err == nil || !strings.Contains(err.Error(), "limit must be non-negative") {
		t.Fatalf("WarmPoolEvents negative limit err = %v, want validation error", err)
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
		case r.Method == http.MethodPost && r.URL.Path == "/v1/images/prepare":
			writeSDKJSON(t, w, sdkImagePrepareResult(req.body["dry_run"] == true))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/images/preparations":
			writeSDKJSON(t, w, ImagePrepareListResult{
				Preparations: []ImagePrepareResult{sdkImagePrepareResult(false)},
				Count:        1,
				Offset:       atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:        atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/images/preparations/image-prepare-1":
			writeSDKJSON(t, w, sdkImagePrepareResult(false))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/images/gc":
			writeSDKJSON(t, w, sdkImageGCResult(req.body["dry_run"] == true))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/images/gc/runs":
			writeSDKJSON(t, w, ImageGCListResult{
				Runs:   []ImageGCResult{sdkImageGCResult(false)},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/images/gc/runs/image-gc-1":
			writeSDKJSON(t, w, sdkImageGCResult(false))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/policies/lifecycle":
			writeSDKJSON(t, w, sdkLifecyclePolicyResult(req.body["dry_run"] == true))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/policies/lifecycle/runs":
			writeSDKJSON(t, w, LifecyclePolicyListResult{
				Runs:   []LifecyclePolicyResult{sdkLifecyclePolicyResult(false)},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/policies/lifecycle/runs/lifecycle-policy-1":
			writeSDKJSON(t, w, sdkLifecyclePolicyResult(false))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/storage/budget":
			writeSDKJSON(t, w, sdkStorageBudgetResult(req.body["dry_run"] == true))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/storage/budget/runs":
			writeSDKJSON(t, w, StorageBudgetListResult{
				Runs:   []StorageBudgetResult{sdkStorageBudgetResult(false)},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/storage/budget/runs/storage-budget-1":
			writeSDKJSON(t, w, sdkStorageBudgetResult(false))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/storage/prune":
			writeSDKJSON(t, w, sdkStoragePruneResult(req.body["dry_run"] == true))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/storage/prune/runs":
			writeSDKJSON(t, w, StoragePruneListResult{
				Runs:   []StoragePruneResult{sdkStoragePruneResult(false)},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/storage/prune/runs/storage-prune-1":
			writeSDKJSON(t, w, sdkStoragePruneResult(false))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/runs":
			writeSDKJSON(t, w, ControllerRunListResult{
				Runs: []ControllerRunSummary{{
					ID:              "storage-prune-1",
					Created:         time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
					Namespace:       "team-a",
					Kind:            "storage.prune",
					TargetType:      "storage",
					TargetID:        "build-scratch",
					AssignmentCount: 1,
					SkipCount:       1,
					Fields:          map[string]string{"older_than": "48h", "apply": "true"},
				}},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/reconcile/plan":
			writeSDKJSON(t, w, sdkReconcilePlan())
		case r.Method == http.MethodPost && r.URL.Path == "/v1/reconcile":
			writeSDKJSON(t, w, sdkReconcileResult())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/summary":
			writeSDKJSON(t, w, sdkOperationsSummary())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/audit":
			writeSDKJSON(t, w, AuditListResult{
				Events: []AuditEvent{sdkAuditEvent()},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/audit/verify":
			writeSDKJSON(t, w, AuditVerifyResult{
				OK:       true,
				Events:   7,
				HeadHash: "hash-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workers":
			writeSDKJSON(t, w, WorkerListResult{
				Workers: []HostRecord{sdkHostRecord()},
				Count:   1,
				Offset:  atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:   atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workers/worker-1/sandboxes":
			writeSDKJSON(t, w, SandboxListResult{
				Sandboxes: []SandboxStatus{sdkWorkerSandbox()},
				Count:     1,
				Offset:    atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:     atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workers/worker-1/events":
			writeSDKJSON(t, w, AuditListResult{
				Events: []AuditEvent{sdkAuditEvent()},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workers/worker-1/reports":
			writeSDKJSON(t, w, AssignmentReportListResult{
				Reports: []AssignmentReport{sdkAssignmentReport()},
				Count:   1,
				Offset:  atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:   atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workers/worker-1/metering":
			writeSDKJSON(t, w, sdkWorkerMetering("worker-1"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workers/worker-1":
			writeSDKJSON(t, w, sdkHostRecord())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/assignments":
			writeSDKJSON(t, w, AssignmentListResult{
				Assignments: []Assignment{sdkInventoryAssignment()},
				Count:       1,
				Offset:      atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:       atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/assignments":
			writeSDKJSON(t, w, sdkCreatedAssignment())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/assignments/assignment-1":
			writeSDKJSON(t, w, sdkInventoryAssignment())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/assignments/assignment-1/events":
			writeSDKJSON(t, w, AuditListResult{
				Events: []AuditEvent{sdkAuditEvent()},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/assignments/assignment-1/reports":
			writeSDKJSON(t, w, AssignmentReportListResult{
				Reports: []AssignmentReport{sdkAssignmentReport()},
				Count:   1,
				Offset:  atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:   atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/assignments/assignment-1/metering":
			writeSDKJSON(t, w, sdkAssignmentMetering("assignment-1"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/assignments/assignment-1/cancel":
			writeSDKJSON(t, w, sdkAssignmentCancel(req.body))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/assignments/assignment-1/retry":
			writeSDKJSON(t, w, sdkAssignmentRetry(req.body))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/service-accounts":
			writeSDKJSON(t, w, ServiceAccountListResult{ServiceAccounts: []ServiceAccount{sdkServiceAccount()}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/service-accounts":
			writeSDKJSON(t, w, ServiceAccountResult{ServiceAccount: ServiceAccount{
				Name:      stringValue(req.body["name"]),
				Namespace: stringValue(req.body["namespace"]),
				Role:      stringValue(req.body["role"]),
				Created:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
				Updated:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
			}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/service-accounts/ci":
			writeSDKJSON(t, w, ServiceAccountResult{ServiceAccount: sdkServiceAccount()})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/oidc-bindings":
			writeSDKJSON(t, w, OIDCBindingListResult{Bindings: []OIDCBinding{sdkOIDCBinding()}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/oidc-bindings":
			writeSDKJSON(t, w, OIDCBindingResult{Binding: OIDCBinding{
				Name:      stringValue(req.body["name"]),
				Issuer:    stringValue(req.body["issuer"]),
				Subject:   stringValue(req.body["subject"]),
				Audience:  stringValue(req.body["audience"]),
				Namespace: stringValue(req.body["namespace"]),
				Role:      stringValue(req.body["role"]),
				JWKSURL:   stringValue(req.body["jwks_url"]),
				KeyIDs:    []string{"kid-1"},
				Created:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
				Updated:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
			}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/oidc-bindings/github-main":
			writeSDKJSON(t, w, OIDCBindingResult{Binding: sdkOIDCBinding()})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/saml-bindings":
			writeSDKJSON(t, w, SAMLBindingListResult{Bindings: []SAMLBinding{sdkSAMLBinding()}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/saml-bindings":
			writeSDKJSON(t, w, SAMLBindingResult{Binding: SAMLBinding{
				Name:              stringValue(req.body["name"]),
				EntityID:          stringValue(req.body["entity_id"]),
				Subject:           stringValue(req.body["subject"]),
				SSOURL:            stringValue(req.body["sso_url"]),
				Audience:          stringValue(req.body["audience"]),
				Namespace:         stringValue(req.body["namespace"]),
				Role:              stringValue(req.body["role"]),
				MetadataURL:       stringValue(req.body["metadata_url"]),
				CertificateSHA256: "sha256-cert",
				Created:           time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
				Updated:           time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/saml-bindings/okta/refresh":
			writeSDKJSON(t, w, SAMLBindingResult{Binding: sdkSAMLBinding()})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/saml-bindings/okta/metadata":
			w.Header().Set("content-type", "application/samlmetadata+xml; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://fleet.example/saml/acs"></md:EntityDescriptor>`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/saml-bindings/okta/login":
			writeSDKJSON(t, w, sdkSAMLLogin(r.URL.Query().Get("relay_state")))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/saml/acs":
			writeSDKJSON(t, w, sdkSAMLSession(stringValue(req.body["relay_state"])))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/saml-bindings/okta":
			writeSDKJSON(t, w, SAMLBindingResult{Binding: sdkSAMLBinding()})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workers/worker-1/cordon":
			record := sdkHostRecord()
			record.Cordoned = true
			record.CordonReason = stringValue(req.body["reason"])
			record.Status = "cordoned"
			writeSDKJSON(t, w, record)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workers/worker-1/uncordon":
			record := sdkHostRecord()
			record.Cordoned = false
			record.CordonReason = ""
			record.CordonedAt = time.Time{}
			record.Status = "ready"
			writeSDKJSON(t, w, record)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workers/worker-1/quarantine":
			record := sdkHostRecord()
			record.Quarantined = true
			record.QuarantineReason = stringValue(req.body["reason"])
			record.Status = "quarantined"
			writeSDKJSON(t, w, record)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workers/worker-1/unquarantine":
			record := sdkHostRecord()
			record.Quarantined = false
			record.QuarantineReason = ""
			record.QuarantinedAt = time.Time{}
			record.Status = "ready"
			writeSDKJSON(t, w, record)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workers/worker-1/evacuate":
			writeSDKJSON(t, w, sdkWorkerEvacuation(req.body))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workers/worker-1/drain":
			writeSDKJSON(t, w, sdkWorkerDrain(stringValue(req.body["reason"])))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workers/worker-1/decommission":
			writeSDKJSON(t, w, sdkWorkerDecommission(req.body))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/warm-pools":
			status := sdkWarmPoolStatus()
			writeSDKJSON(t, w, WarmPoolResult{
				Pool:    status,
				Created: []Assignment{status.Assignments[0]},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/warm-pools":
			writeSDKJSON(t, w, WarmPoolListResult{WarmPools: []WarmPoolStatus{sdkWarmPoolStatus()}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/warm-pools/runner/events":
			writeSDKJSON(t, w, SandboxEventListResult{
				Events: []SandboxEvent{{
					ID:           "audit-warm-1",
					Time:         time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
					Namespace:    "team-a",
					Actor:        "service-account:ci",
					Action:       "warm_pool.claim",
					TargetType:   "warm_pool",
					TargetID:     "runner",
					WorkerID:     "worker-1",
					AssignmentID: "claim-1",
				}},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/warm-pools/runner":
			writeSDKJSON(t, w, sdkWarmPoolStatus())
		case r.Method == http.MethodPost && r.URL.Path == "/v1/warm-pools/claim":
			status := sdkWarmPoolStatus()
			writeSDKJSON(t, w, WarmPoolClaimResult{
				Namespace:  "team-a",
				Pool:       "runner",
				VMName:     "cove-warm-runner",
				Slot:       status.Assignments[0],
				Assignment: Assignment{ID: "claim-1", Namespace: "team-a", WorkerID: "worker-1", WarmPoolSlot: "warm-slot-1", Verb: "cove", Args: []string{"shell"}, Status: "pending"},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/warm-pools/runner":
			writeSDKJSON(t, w, WarmPoolDeleteResult{
				Namespace: "team-a",
				Pool:      "runner",
				Cleanup:   []Assignment{{ID: "cleanup-1", Namespace: "team-a", WorkerID: "worker-1", WarmPoolSlot: "warm-slot-1", Verb: "cove", Args: []string{"ctl", "stop"}, Status: "pending"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/placements/plan":
			writeSDKJSON(t, w, sdkPlacementPlan())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/placements/plans":
			writeSDKJSON(t, w, PlacementPlanListResult{
				Plans:  []PlacementPlan{sdkPlacementPlan()},
				Count:  1,
				Offset: atoiDefault(r.URL.Query().Get("offset"), 0),
				Limit:  atoiDefault(r.URL.Query().Get("limit"), 0),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/placements/plans/placement-plan-1":
			writeSDKJSON(t, w, sdkPlacementPlan())
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			writeSDKJSON(t, w, SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", RequiredCapabilities: []string{"ram-overlay"}, Status: "pending"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes":
			queueExpires := time.Date(2026, 5, 31, 10, 10, 0, 0, time.UTC)
			retryAt := time.Date(2026, 5, 31, 10, 0, 12, 0, time.UTC)
			writeSDKJSON(t, w, map[string]any{
				"sandboxes": []SandboxStatus{{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", ImageRef: "base:v1", ImageManifestDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111", ImageDigestRef: "ghcr.io/me/dev-vm@sha256:1111111111111111111111111111111111111111111111111111111111111111", ImagePlatform: "darwin/arm64", RequiredCapabilities: []string{"ram-overlay"}, Status: "pending", QueueExpires: queueExpires, QueueAgeMillis: 1500, QueueRemainingMillis: 8500, MaxAttempts: 3, Attempt: 1, RetryDelay: "20s", RetryAt: retryAt, RetryRemainingMillis: 12000}},
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
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/start":
			writeSDKJSON(t, w, SandboxStartResult{
				Namespace:  "team-a",
				ID:         "job-1",
				VMName:     "cove-sandbox-job-1",
				Status:     "pending",
				Started:    true,
				Assignment: sdkInventoryAssignment(),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/stop":
			cleanup := sdkMaintenanceAssignment("cleanup-1", "ctl", "-vm", "cove-sandbox-job-1", "stop")
			writeSDKJSON(t, w, SandboxStopResult{
				Namespace:           "team-a",
				ID:                  "job-1",
				VMName:              "cove-sandbox-job-1",
				Status:              "draining",
				Assignment:          sdkInventoryAssignment(),
				Cleanup:             &cleanup,
				CanceledAssignments: []string{"exec-1", "control-1"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/restart":
			cleanup := sdkMaintenanceAssignment("cleanup-1", "ctl", "-vm", "cove-sandbox-job-1", "stop")
			writeSDKJSON(t, w, SandboxRestartResult{
				Namespace:           "team-a",
				ID:                  "job-1",
				VMName:              "cove-sandbox-job-1",
				Status:              "restarting",
				Restarting:          true,
				Assignment:          sdkInventoryAssignment(),
				Cleanup:             &cleanup,
				CanceledAssignments: []string{"exec-1"},
			})
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
			cleanup := sdkMaintenanceAssignment("cleanup-1", "ctl", "-vm", "cove-sandbox-job-1", "stop")
			writeSDKJSON(t, w, SandboxDeleteResult{
				Namespace:           "team-a",
				ID:                  "job-1",
				VMName:              "cove-sandbox-job-1",
				Status:              "draining",
				Assignment:          sdkInventoryAssignment(),
				Cleanup:             &cleanup,
				CanceledAssignments: []string{"exec-1", "control-1"},
			})
		default:
			http.NotFound(w, r)
		}
	})
	server.Server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func sdkImagePrepareResult(dryRun bool) ImagePrepareResult {
	const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	digestRef := "ghcr.io/me/base@" + digest
	return ImagePrepareResult{
		ID:                  "image-prepare-1",
		Namespace:           "team-a",
		SourceRef:           digestRef,
		ImageRef:            "base:v1",
		ImageManifestDigest: digest,
		ImageDigestRef:      digestRef,
		ImagePlatform:       "darwin/arm64",
		RequiredLabels:      map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{
			"ram-overlay",
			"asif",
		},
		DryRun: dryRun,
		Assignments: []Assignment{{
			ID:                  "assignment-prepare-1",
			Namespace:           "team-a",
			WorkerID:            "worker-1",
			ImageRef:            "base:v1",
			ImageManifestDigest: digest,
			ImageDigestRef:      digestRef,
			ImagePlatform:       "darwin/arm64",
			RequiredLabels:      map[string]string{"zone": "desk"},
			RequiredCapabilities: []string{
				"ram-overlay",
				"asif",
			},
			Verb:   "cove",
			Args:   []string{"image", "pull", "-tag", "base:v1", "-force", digestRef},
			Status: "pending",
		}},
		Skipped: []ImagePrepareSkip{
			{WorkerID: "worker-2", Reason: "present"},
			{WorkerID: "worker-3", Reason: "label", MissingLabels: map[string]string{"zone": "desk"}},
			{WorkerID: "worker-4", Reason: "capability", MissingCapabilities: []string{"asif"}},
		},
	}
}

func sdkPlacementPlan() PlacementPlan {
	return PlacementPlan{
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
	}
}

func sdkReconcilePlan() ReconcileResult {
	return ReconcileResult{
		StaleWorkers:        []string{"worker-2"},
		RequeuedAssignments: []string{"assignment-1"},
		ExpiredAssignments:  []string{"assignment-expired"},
		WarmPoolAssignments: []string{"warm-slot-1"},
	}
}

func sdkReconcileResult() ReconcileResult {
	return ReconcileResult{
		StaleWorkers:        []string{"worker-2"},
		RequeuedAssignments: []string{"assignment-1"},
		ReplacedAssignments: []string{"assignment-2"},
		ExpiredAssignments:  []string{"assignment-expired"},
		WarmPoolAssignments: []string{"warm-slot-1"},
		WarmPoolCanceled:    []string{"warm-slot-2"},
		WarmPoolCleanup:     []string{"cleanup-1"},
	}
}

func sdkServiceAccount() ServiceAccount {
	return ServiceAccount{
		Name:      "ci",
		Namespace: "team-a",
		Role:      "operator",
		Created:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Updated:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
	}
}

func sdkOIDCBinding() OIDCBinding {
	return OIDCBinding{
		Name:      "github-main",
		Issuer:    "https://token.actions.githubusercontent.com",
		Subject:   "repo:tmc/cove:ref:refs/heads/main",
		Audience:  "cove-fleet",
		Namespace: "team-a",
		Role:      "operator",
		JWKSURL:   "https://token.actions.githubusercontent.com/.well-known/jwks",
		KeyIDs:    []string{"kid-1"},
		Created:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Updated:   time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
	}
}

func sdkSAMLBinding() SAMLBinding {
	return SAMLBinding{
		Name:              "okta",
		EntityID:          "https://idp.example/saml",
		Subject:           "ci@example.com",
		SSOURL:            "https://idp.example/sso",
		Audience:          "https://fleet.example/saml/acs",
		Namespace:         "team-a",
		Role:              "operator",
		MetadataURL:       "https://idp.example/metadata.xml",
		CertificateSHA256: "sha256-cert",
		Created:           time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Updated:           time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
	}
}

func sdkSAMLLogin(relayState string) SAMLAuthnRequestResult {
	return SAMLAuthnRequestResult{
		Binding:      sdkSAMLBinding(),
		RequestID:    "_saml-request-1",
		IssueInstant: time.Date(2026, 5, 31, 10, 1, 0, 0, time.UTC),
		RelayState:   relayState,
		XML:          "<AuthnRequest/>",
		SAMLRequest:  "encoded-request",
		RedirectURL:  "https://idp.example/sso?SAMLRequest=encoded-request",
	}
}

func sdkSAMLSession(relayState string) SAMLSessionResult {
	return SAMLSessionResult{
		Token:      "saml-session-token",
		Expires:    time.Date(2026, 5, 31, 11, 0, 0, 0, time.UTC),
		Binding:    sdkSAMLBinding(),
		Subject:    "ci@example.com",
		RelayState: relayState,
	}
}

func sdkImageGCResult(dryRun bool) ImageGCResult {
	return ImageGCResult{
		ID:                   "image-gc-1",
		Created:              time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Namespace:            "team-a",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif"},
		OlderThan:            "168h",
		Apply:                true,
		DryRun:               dryRun,
		Assignments:          []Assignment{sdkMaintenanceAssignment("assignment-image-gc-1", "image", "gc", "-yes", "-older-than", "168h")},
		Skipped: []ImageGCSkip{
			{WorkerID: "worker-2", Reason: "status", Status: "cordoned"},
			{WorkerID: "worker-3", Reason: "label", MissingLabels: map[string]string{"zone": "desk"}},
			{WorkerID: "worker-4", Reason: "capability", MissingCapabilities: []string{"asif"}},
		},
	}
}

func sdkLifecyclePolicyResult(dryRun bool) LifecyclePolicyResult {
	return LifecyclePolicyResult{
		ID:                   "lifecycle-policy-1",
		Created:              time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Namespace:            "team-a",
		VMName:               "ci-runner",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay", "asif"},
		IdleTimeout:          "30m",
		RunBudget:            100,
		DryRun:               dryRun,
		Assignments:          []Assignment{sdkMaintenanceAssignment("assignment-lifecycle-policy-1", "policy", "ci-runner", "set", "-idle-timeout", "30m", "-run-budget", "100")},
		Skipped: []LifecyclePolicySkip{
			{WorkerID: "worker-2", Reason: "status", Status: "cordoned"},
			{WorkerID: "worker-3", Reason: "label", MissingLabels: map[string]string{"zone": "desk"}},
			{WorkerID: "worker-4", Reason: "capability", MissingCapabilities: []string{"asif"}},
		},
	}
}

func sdkStorageBudgetResult(dryRun bool) StorageBudgetResult {
	warn := 70
	hard := 90
	return StorageBudgetResult{
		ID:                   "storage-budget-1",
		Created:              time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Namespace:            "team-a",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay"},
		Target:               "750GB",
		WarnPct:              &warn,
		HardPct:              &hard,
		DryRun:               dryRun,
		Assignments:          []Assignment{sdkMaintenanceAssignment("assignment-storage-budget-1", "storage", "budget", "set", "-target", "750GB", "-warn", "70", "-hard", "90")},
		Skipped: []StoragePolicySkip{
			{WorkerID: "worker-2", Reason: "status", Status: "cordoned"},
			{WorkerID: "worker-3", Reason: "label", MissingLabels: map[string]string{"zone": "desk"}},
			{WorkerID: "worker-4", Reason: "capability", MissingCapabilities: []string{"ram-overlay"}},
		},
	}
}

func sdkStoragePruneResult(dryRun bool) StoragePruneResult {
	return StoragePruneResult{
		ID:                   "storage-prune-1",
		Created:              time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Namespace:            "team-a",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay"},
		Category:             "build-scratch",
		OlderThan:            "48h",
		Apply:                true,
		DryRun:               dryRun,
		Assignments:          []Assignment{sdkMaintenanceAssignment("assignment-storage-prune-1", "storage", "prune", "build-scratch", "-apply", "-older-than", "48h")},
		Skipped: []StoragePolicySkip{
			{WorkerID: "worker-2", Reason: "status", Status: "cordoned"},
			{WorkerID: "worker-3", Reason: "label", MissingLabels: map[string]string{"zone": "desk"}},
			{WorkerID: "worker-4", Reason: "capability", MissingCapabilities: []string{"ram-overlay"}},
		},
	}
}

func sdkMaintenanceAssignment(id string, args ...string) Assignment {
	return Assignment{
		ID:                   id,
		Namespace:            "team-a",
		WorkerID:             "worker-1",
		RequiredLabels:       map[string]string{"zone": "desk"},
		RequiredCapabilities: []string{"ram-overlay"},
		Verb:                 "cove",
		Args:                 args,
		Status:               "pending",
	}
}

func sdkOperationsSummary() OperationsSummary {
	now := time.Date(2026, 5, 31, 10, 5, 0, 0, time.UTC)
	active := sdkMaintenanceAssignment("assignment-storage-prune-1", "storage", "prune", "build-scratch", "-apply", "-older-than", "48h")
	active.Status = "running"
	return OperationsSummary{
		Time:      now,
		Namespace: "team-a",
		Workers: WorkerOperationsSummary{
			Total:       3,
			Ready:       1,
			Cordoned:    1,
			Quarantined: 1,
			ByStatus:    map[string]int{"ready": 1, "cordoned": 1, "quarantined": 1},
			Capabilities: []WorkerCapabilitySummary{
				{Name: "asif", Total: 2, Ready: 1, Cordoned: 1, ByStatus: map[string]int{"ready": 1, "cordoned": 1}, Workers: []string{"worker-1", "worker-2"}},
				{Name: "ram-overlay", Total: 2, Ready: 1, Quarantined: 1, ByStatus: map[string]int{"ready": 1, "quarantined": 1}, Workers: []string{"worker-1", "worker-3"}},
			},
			Attention: []HostRecord{{
				ID:           "worker-2",
				Host:         "mini-2",
				Version:      "dev",
				Capabilities: []string{"asif"},
				Status:       "cordoned",
				Cordoned:     true,
				CordonReason: "maintenance",
				LastSeen:     now,
				Expires:      now.Add(time.Minute),
			}},
		},
		Assignments: AssignmentOperationsSummary{
			Total:             2,
			Active:            1,
			Terminal:          1,
			ByStatus:          map[string]int{"running": 1, "complete": 1},
			ActiveAssignments: []Assignment{active},
		},
		Sandboxes: SandboxOperationsSummary{
			Total:    1,
			Active:   1,
			ByStatus: map[string]int{"ready": 1},
			ActiveSandboxes: []SandboxStatus{{
				Namespace: "team-a",
				ID:        "job-1",
				Status:    "ready",
				WorkerID:  "worker-1",
			}},
		},
		WarmPools: WarmPoolOperationsSummary{
			Total:    1,
			Desired:  2,
			Slots:    1,
			Active:   1,
			Ready:    1,
			ByStatus: map[string]int{"ready": 1},
			Pools:    []WarmPoolStatus{sdkWarmPoolStatus()},
		},
		Metering: MeteringSummary{Namespace: "team-a", Records: 2, DurationMillis: 2000, VMMillis: 2000},
	}
}

func sdkAuditEvent() AuditEvent {
	return AuditEvent{
		ID:           "audit-1",
		Time:         time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Namespace:    "team-a",
		Actor:        "service-account:ci",
		Action:       "assignment.create",
		TargetType:   "assignment",
		TargetID:     "assignment-1",
		WorkerID:     "worker-1",
		AssignmentID: "assignment-1",
		Status:       "pending",
		Fields:       map[string]string{"reason": "created"},
		PrevHash:     "prev-1",
		Hash:         "hash-1",
	}
}

func sdkWorkerSandbox() SandboxStatus {
	return SandboxStatus{
		Namespace: "team-a",
		ID:        "job-1",
		VMName:    "cove-sandbox-job-1",
		ImageRef:  "base:v1",
		Status:    "ready",
		WorkerID:  "worker-1",
	}
}

func sdkAssignmentReport() AssignmentReport {
	now := time.Date(2026, 5, 31, 10, 5, 0, 0, time.UTC)
	return AssignmentReport{
		Namespace:    "team-a",
		AssignmentID: "assignment-1",
		WorkerID:     "worker-1",
		Status:       "complete",
		Created:      now.Add(-time.Minute),
		Updated:      now,
		Report:       WorkerReport{AssignmentID: "assignment-1", Status: "complete", ExitCode: 7, Stdout: "out", Stderr: "err", Time: now},
	}
}

func sdkWorkerMetering(id string) MeteringResult {
	result := sdkMetering("job-1")
	result.Summary.WorkerID = id
	for i := range result.Records {
		result.Records[i].WorkerID = id
		result.Records[i].Resources = Capacity{VMs: 1, CPUs: 4}
	}
	return result
}

func sdkAssignmentMetering(id string) MeteringResult {
	result := sdkWorkerMetering("worker-1")
	result.Summary.AssignmentID = id
	for i := range result.Records {
		result.Records[i].AssignmentID = id
	}
	return result
}

func sdkHostRecord() HostRecord {
	now := time.Date(2026, 5, 31, 10, 5, 0, 0, time.UTC)
	return HostRecord{
		ID:           "worker-1",
		Host:         "mini-1",
		Address:      "ssh://mini-1",
		Version:      "dev",
		Labels:       map[string]string{"zone": "desk", "role": "runner"},
		Capabilities: []string{"ram-overlay", "asif"},
		ImageRefs:    []string{"base:v1"},
		ImageDetails: []WorkerImage{{
			Ref:                  "base:v1",
			SourceManifestDigest: "sha256:base",
		}},
		Capacity:     Capacity{VMs: 1, MaxVMs: 4},
		Status:       "ready",
		Cordoned:     true,
		CordonReason: "maintenance",
		LastSeen:     now,
		Expires:      now.Add(time.Minute),
		Report:       &WorkerReport{ID: "report-1", Status: "running", Time: now},
	}
}

func sdkInventoryAssignment() Assignment {
	assignment := sdkMaintenanceAssignment("assignment-1", "run", "-fork-from", "base:v1", "-ephemeral")
	assignment.ImageRef = "base:v1"
	assignment.WarmPool = "runner"
	assignment.SandboxID = "job-1"
	assignment.Status = "running"
	assignment.LeasedTo = "worker-1"
	assignment.LeaseExpires = time.Date(2026, 5, 31, 10, 6, 0, 0, time.UTC)
	return assignment
}

func sdkCreatedAssignment() Assignment {
	assignment := sdkMaintenanceAssignment("assignment-created", "run", "-fork-from", "base:v1", "-ephemeral")
	assignment.Policy = "bin-pack"
	assignment.ImageRef = "base:v1"
	assignment.ManifestBundle = "manifests"
	assignment.ImageManifestDigest = "sha256:base"
	assignment.ImageDigestRef = "ghcr.io/me/base@sha256:base"
	assignment.ImagePlatform = "darwin/arm64"
	assignment.RequiredCapabilities = []string{"ram-overlay", "asif"}
	assignment.AntiAffinityKey = "ci/buildkite"
	assignment.Resources = Capacity{VMs: 1, CPUs: 4}
	assignment.Priority = 8
	assignment.QueueExpires = time.Date(2026, 5, 31, 10, 10, 0, 0, time.UTC)
	assignment.MaxAttempts = 4
	assignment.RetryDelay = "30s"
	return assignment
}

func sdkAssignmentCancel(body map[string]any) AssignmentCancelResult {
	assignment := sdkInventoryAssignment()
	assignment.Status = "canceled"
	assignment.LeasedTo = ""
	assignment.LeaseExpires = time.Time{}
	return AssignmentCancelResult{
		Assignment:     assignment,
		Reason:         stringValue(body["reason"]),
		Force:          body["force"] == true,
		Canceled:       true,
		PreviousStatus: "running",
	}
}

func sdkAssignmentRetry(body map[string]any) AssignmentRetryResult {
	assignment := sdkInventoryAssignment()
	assignment.Status = "pending"
	assignment.LeasedTo = ""
	assignment.LeaseExpires = time.Time{}
	assignment.WorkerID = stringValue(body["worker_id"])
	if assignment.WorkerID == "" {
		assignment.WorkerID = "worker-1"
	}
	return AssignmentRetryResult{
		Assignment:       assignment,
		Reason:           stringValue(body["reason"]),
		PreviousStatus:   "failed",
		PreviousWorkerID: "worker-1",
		Replanned:        body["replan"] == true || assignment.WorkerID != "worker-1",
	}
}

func sdkWorkerEvacuation(body map[string]any) WorkerEvacuationResult {
	apply := body["apply"] == true
	force := body["force"] == true
	reason := stringValue(body["reason"])
	result := WorkerEvacuationResult{
		Worker: sdkHostRecord(),
		Reason: reason,
		Apply:  apply,
		Force:  force,
		Assignments: []WorkerEvacuationAssignment{
			{
				AssignmentID:   "assignment-1",
				Namespace:      "team-a",
				Status:         "pending",
				WorkerID:       "worker-1",
				Action:         "requeue",
				TargetWorkerID: "worker-2",
				Candidates:     []PlacementCandidate{{Rank: 1, WorkerID: "worker-2", RequestedVMs: 1}},
			},
			{
				AssignmentID: "assignment-2",
				Namespace:    "team-a",
				Status:       "running",
				WorkerID:     "worker-1",
				Action:       "blocked",
				Reason:       "active assignment",
			},
		},
		Blocked: []WorkerEvacuationAssignment{{
			AssignmentID: "assignment-2",
			Namespace:    "team-a",
			Status:       "running",
			WorkerID:     "worker-1",
			Action:       "blocked",
			Reason:       "active assignment",
		}},
	}
	if apply {
		result.Applied = true
		requeued := sdkInventoryAssignment()
		requeued.WorkerID = "worker-2"
		result.Requeued = []Assignment{requeued}
		result.Canceled = []string{"assignment-3"}
	}
	return result
}

func sdkWorkerDrain(reason string) WorkerDrainResult {
	record := sdkHostRecord()
	record.Cordoned = true
	record.CordonReason = reason
	record.Status = "cordoned"
	assignment := sdkInventoryAssignment()
	assignment.Status = "draining"
	return WorkerDrainResult{
		Worker: record,
		Sandboxes: []SandboxStopResult{{
			Namespace:  "team-a",
			ID:         "job-1",
			VMName:     "job-1",
			Status:     "draining",
			Canceled:   true,
			Assignment: assignment,
		}},
		Skipped: []WorkerDrainSkip{{SandboxID: "job-2", Status: "complete", Reason: "terminal"}},
	}
}

func sdkWorkerDecommission(body map[string]any) WorkerDecommissionResult {
	return WorkerDecommissionResult{
		Worker:   sdkHostRecord(),
		Reason:   stringValue(body["reason"]),
		Force:    body["force"] == true,
		Removed:  true,
		Canceled: []string{"assignment-1"},
	}
}

func sdkWarmPoolStatus() WarmPoolStatus {
	return WarmPoolStatus{
		WarmPool: WarmPool{
			Namespace:            "team-a",
			Name:                 "runner",
			ImageRef:             "base:v1",
			ImagePlatform:        "darwin/arm64",
			Size:                 2,
			Policy:               "bin-pack",
			RequiredLabels:       map[string]string{"zone": "desk"},
			RequiredCapabilities: []string{"ram-overlay", "asif"},
			Resources:            Capacity{VMs: 1, CPUs: 4},
			Args:                 []string{"-memory", "8G"},
		},
		Slots:    1,
		Active:   1,
		Ready:    1,
		ByStatus: map[string]int{"ready": 1},
		Assignments: []Assignment{{
			ID:                   "warm-slot-1",
			Namespace:            "team-a",
			WorkerID:             "worker-1",
			WarmPool:             "runner",
			Policy:               "bin-pack",
			ImageRef:             "base:v1",
			RequiredCapabilities: []string{"ram-overlay", "asif"},
			Resources:            Capacity{VMs: 1, CPUs: 4},
			Verb:                 "cove",
			Args:                 []string{"run", "-fork-from", "base:v1"},
			Status:               "ready",
		}},
	}
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

func stringValue(value any) string {
	s, _ := value.(string)
	return s
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
