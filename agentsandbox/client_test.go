package agentsandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCloudClientCreateExecControlDelete(t *testing.T) {
	server := newSDKFleetServer(t)
	ctx := context.Background()
	client, err := Create(ctx, ClientOptions{
		Provider:  ProviderCloud,
		FleetURL:  server.URL,
		APIKey:    "secret",
		Namespace: "team-a",
		SandboxID: "job-1",
		ImageRef:  "base:v1",
		Timeout:   time.Second,
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
	if len(list) != 1 || list[0].ID != "job-1" || list[0].ImageRef != "base:v1" {
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
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			writeSDKJSON(t, w, SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "pending"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes":
			writeSDKJSON(t, w, map[string]any{"sandboxes": []SandboxStatus{{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", ImageRef: "base:v1", Status: "ready"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/wait":
			writeSDKJSON(t, w, WaitResult{Done: true, Sandbox: SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "ready"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/job-1/lease":
			lease := Lease{Holder: "runner-42", Expires: time.Now().Add(time.Minute)}
			writeSDKJSON(t, w, LeaseResult{Sandbox: SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "ready", Lease: &lease}, Lease: lease})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/job-1/lease":
			writeSDKJSON(t, w, LeaseResult{Sandbox: SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "ready"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/job-1":
			writeSDKJSON(t, w, SandboxStatus{Namespace: "team-a", ID: "job-1", VMName: "cove-sandbox-job-1", Status: "ready"})
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
