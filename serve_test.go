package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/control/operations"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// newServeTestRegistry returns an in-memory OperationRegistry for testing.
func newServeTestRegistry(t *testing.T) *operations.OperationRegistry {
	t.Helper()
	reg, err := operations.NewOperationRegistry(operations.NewMemOperationStore())
	if err != nil {
		t.Fatalf("NewOperationRegistry: %v", err)
	}
	return reg
}

// TestGatewayHealthz verifies /healthz returns 200 with no auth.
func TestGatewayHealthz(t *testing.T) {
	dir := t.TempDir()
	gw, err := NewGateway(dir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	gw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz: got %d, want 200", rec.Code)
	}
}

// TestGatewayEmptyVMDir verifies that an empty VM dir yields zero routes and
// /healthz still works.
func TestGatewayEmptyVMDir(t *testing.T) {
	dir := t.TempDir()
	gw, err := NewGateway(dir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.mu.RLock()
	n := len(gw.routes)
	gw.mu.RUnlock()
	if n != 0 {
		t.Errorf("expected 0 routes, got %d", n)
	}
}

// TestGatewayNonListeningSocket verifies that a VM directory with a non-listening
// socket file is not added to the route table.
func TestGatewayNonListeningSocket(t *testing.T) {
	vmDir := t.TempDir()
	vmSubDir := filepath.Join(vmDir, "test-vm")
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Create the socket file without actually binding it.
	sockPath := filepath.Join(vmSubDir, "control.sock")
	f, err := os.Create(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	gw, err := NewGateway(vmDir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.mu.RLock()
	_, found := gw.routes["test-vm"]
	gw.mu.RUnlock()
	if found {
		t.Error("non-listening socket should not be added to routes")
	}
}

// TestGatewayMasterAuth verifies that authenticated routes reject missing/wrong tokens.
func TestGatewayMasterAuth(t *testing.T) {
	dir := t.TempDir()
	const masterTok = "master-secret-token"
	gw, err := NewGateway(dir, masterTok, false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	tests := []struct {
		name     string
		token    string
		wantCode int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"wrong token", "wrong", http.StatusUnauthorized},
		{"correct token", masterTok, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/vms", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			gw.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

// TestGatewayPerVMAuth verifies that -per-vm-auth mode rejects the master token
// and accepts the per-VM token.
func TestGatewayPerVMAuth(t *testing.T) {
	// Use a short base path to avoid Unix socket path length limits (104 chars).
	vmDir, err := os.MkdirTemp("", "gwpvma*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(vmDir) })

	vmName := "vm"
	vmSubDir := filepath.Join(vmDir, vmName)
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Start a Unix socket that accepts+closes immediately (so proxy gets EOF quickly).
	sockPath := filepath.Join(vmSubDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close() // immediate EOF → gateway gets 502 quickly
		}
	}()

	// Write a per-VM token file.
	const perVMTok = "per-vm-secret"
	tokenPath := filepath.Join(vmSubDir, controlTokenFileName)
	if err := os.WriteFile(tokenPath, []byte(perVMTok+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	const masterTok = "master-token"
	gw, err := NewGateway(vmDir, masterTok, true /* perVMAuth */, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// Ensure the route is registered (refresh uses the real socket which is bound).
	gw.refresh()

	gw.mu.RLock()
	_, ok := gw.routes[vmName]
	gw.mu.RUnlock()
	if !ok {
		t.Fatal("route not registered after refresh")
	}

	path := "/v1/vms/" + vmName + "/status"

	t.Run("master token rejected in per-vm-auth mode", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+masterTok)
		gw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", rec.Code)
		}
	})

	t.Run("per-vm token accepted in per-vm-auth mode", func(t *testing.T) {
		// The gateway will dial the socket; since no one is accepting, it will
		// return 502 Bad Gateway — but that proves it passed auth.
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+perVMTok)
		gw.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Error("per-VM token should pass auth, got 401")
		}
	})
}

func TestGatewaySnapshotRoutesProxyControlRequest(t *testing.T) {
	vmDir, err := os.MkdirTemp("", "gwsnap*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(vmDir) })

	const (
		vmName = "vm"
		token  = "master-token"
	)
	vmSubDir := filepath.Join(vmDir, vmName)
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}

	reqCh := make(chan *controlpb.ControlRequest, 8)
	errCh := make(chan error, 1)
	done := make(chan struct{})
	sockPath := filepath.Join(vmSubDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
				default:
					errCh <- err
				}
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var got controlpb.ControlRequest
				if err := protojsonUnmarshaler.Unmarshal(scanner.Bytes(), &got); err != nil {
					errCh <- err
					return
				}
				reqCh <- &got
				resp := &controlpb.ControlResponse{
					Success: true,
					Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "ok"}},
				}
				data, err := protojsonMarshaler.Marshal(resp)
				if err != nil {
					errCh <- err
					return
				}
				fmt.Fprintf(conn, "%s\n", data)
			}(conn)
		}
	}()

	gw, err := NewGateway(vmDir, token, false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.mu.RLock()
	_, ok := gw.routes[vmName]
	gw.mu.RUnlock()
	if !ok {
		t.Fatal("route not registered")
	}

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantAction string
		wantName   string
	}{
		{
			name:       "save",
			method:     http.MethodPost,
			path:       "/v1/vms/vm/snapshot",
			body:       `{"name":"checkpoint1"}`,
			wantAction: "save",
			wantName:   "checkpoint1",
		},
		{
			name:       "list",
			method:     http.MethodGet,
			path:       "/v1/vms/vm/snapshots",
			wantAction: "list",
		},
		{
			name:       "restore",
			method:     http.MethodPost,
			path:       "/v1/vms/vm/snapshots/checkpoint1/restore",
			wantAction: "restore",
			wantName:   "checkpoint1",
		},
		{
			name:       "delete",
			method:     http.MethodDelete,
			path:       "/v1/vms/vm/snapshots/checkpoint1",
			wantAction: "delete",
			wantName:   "checkpoint1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+token)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			gw.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s: got %d, want 200; body: %s", tt.method, tt.path, rec.Code, rec.Body.String())
			}

			var got *controlpb.ControlRequest
			select {
			case got = <-reqCh:
			case err := <-errCh:
				t.Fatalf("socket server: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for proxied control request")
			}
			if got.Type != "snapshot" {
				t.Fatalf("request type = %q, want snapshot", got.Type)
			}
			snapshot := got.GetSnapshot()
			if snapshot == nil {
				t.Fatal("snapshot payload is nil")
			}
			if snapshot.Action != tt.wantAction {
				t.Fatalf("snapshot action = %q, want %q", snapshot.Action, tt.wantAction)
			}
			if snapshot.Name != tt.wantName {
				t.Fatalf("snapshot name = %q, want %q", snapshot.Name, tt.wantName)
			}
		})
	}
}

// TestGatewayLROCreateVM verifies POST /v1/vms returns 202 + op ID,
// and after 200ms the operation reaches failed/not_implemented.
func TestGatewayLROCreateVM(t *testing.T) {
	dir := t.TempDir()
	const tok = "lro-token"
	reg := newServeTestRegistry(t)
	gw, err := NewGateway(dir, tok, false, nil, reg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/vms", strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/vms: got %d, want 202", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/v1/operations/op_") {
		t.Fatalf("Location header: %q", loc)
	}

	var opResp operations.Operation
	if err := json.NewDecoder(rec.Body).Decode(&opResp); err != nil {
		t.Fatalf("decode 202 body: %v", err)
	}
	if opResp.Status != "pending" {
		t.Errorf("initial status: %q, want pending", opResp.Status)
	}

	// Wait for the not_implemented goroutine.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		op, ok := reg.Get(opResp.ID)
		if ok && op.Status == "failed" {
			if op.Error == nil || op.Error.Code != "not_implemented" {
				t.Errorf("error code: %+v", op.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("operation did not reach failed/not_implemented within 500ms")
}

// TestGatewayListOperations verifies GET /v1/operations lists ops.
func TestGatewayListOperations(t *testing.T) {
	dir := t.TempDir()
	const tok = "list-ops-token"
	reg := newServeTestRegistry(t)
	gw, err := NewGateway(dir, tok, false, nil, reg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	// Create one operation.
	op, err := reg.Create("vms/test")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/operations", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/operations: got %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) == 0 {
		t.Errorf("expected at least one operation, got 0 (created: %s)", op.ID)
	}
}

// TestGatewayGetOperation verifies GET /v1/operations/<id>.
func TestGatewayGetOperation(t *testing.T) {
	dir := t.TempDir()
	const tok = "get-op-token"
	reg := newServeTestRegistry(t)
	gw, err := NewGateway(dir, tok, false, nil, reg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	op, err := reg.Create("vms/test")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/operations/"+op.ID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/operations/%s: got %d", op.ID, rec.Code)
	}
	var got operations.Operation
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != op.ID {
		t.Errorf("ID: got %q, want %q", got.ID, op.ID)
	}
}

// TestRunHTTPStartHTTP verifies that StartHTTP binds a listener and /healthz
// returns 200 without a real VM.
func TestRunHTTPStartHTTP(t *testing.T) {
	cs := &ControlServer{
		authToken: "test-run-token",
	}

	ln, err := cs.StartHTTP(":0", "test-vm")
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer ln.Close()

	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", ln.Addr()))
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: got %d, want 200", resp.StatusCode)
	}
}

// TestRunHTTPAuthToken verifies that authenticated endpoints require the token.
func TestRunHTTPAuthToken(t *testing.T) {
	const tok = "run-auth-token"
	cs := &ControlServer{
		authToken: tok,
	}

	ln, err := cs.StartHTTP(":0", "test-vm")
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer ln.Close()

	base := fmt.Sprintf("http://%s", ln.Addr())

	t.Run("no token", func(t *testing.T) {
		resp, err := http.Get(base + "/v1/vms/test-vm/status")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.StatusCode)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/vms/test-vm/status", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.StatusCode)
		}
	})
}

// TestSharedHostWarningSingleUser verifies no warning is emitted for one user.
func TestSharedHostWarningSingleUser(t *testing.T) {
	var buf bytes.Buffer
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	checkSharedHost(false, "", func() ([]string, error) {
		return []string{"alice"}, nil
	})

	w.Close()
	os.Stderr = orig
	io.Copy(&buf, r)

	if buf.Len() > 0 {
		t.Errorf("expected no warning for single user, got: %q", buf.String())
	}
}

// TestSharedHostWarningMultipleUsers verifies a warning is emitted for >1 user.
func TestSharedHostWarningMultipleUsers(t *testing.T) {
	var buf bytes.Buffer
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	checkSharedHost(false, "", func() ([]string, error) {
		return []string{"alice", "bob"}, nil
	})

	w.Close()
	os.Stderr = orig
	io.Copy(&buf, r)

	if !strings.Contains(buf.String(), "2 distinct logged-in users") {
		t.Errorf("expected multi-user warning, got: %q", buf.String())
	}
	// Usernames must NOT appear in the warning.
	if strings.Contains(buf.String(), "alice") || strings.Contains(buf.String(), "bob") {
		t.Errorf("warning must not include usernames, got: %q", buf.String())
	}
}

// TestSharedHostWarningPerVMAuthSkips verifies -per-vm-auth suppresses the warning.
func TestSharedHostWarningPerVMAuthSkips(t *testing.T) {
	var buf bytes.Buffer
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	checkSharedHost(true /* perVMAuth */, "", func() ([]string, error) {
		return []string{"alice", "bob"}, nil
	})

	w.Close()
	os.Stderr = orig
	io.Copy(&buf, r)

	if buf.Len() > 0 {
		t.Errorf("expected no warning in per-vm-auth mode, got: %q", buf.String())
	}
}

// TestSharedHostWarningTokenFileSkips verifies -token-file suppresses the warning.
func TestSharedHostWarningTokenFileSkips(t *testing.T) {
	var buf bytes.Buffer
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	checkSharedHost(false, "/tmp/mytoken", func() ([]string, error) {
		return []string{"alice", "bob"}, nil
	})

	w.Close()
	os.Stderr = orig
	io.Copy(&buf, r)

	if buf.Len() > 0 {
		t.Errorf("expected no warning with token-file, got: %q", buf.String())
	}
}

// TestSharedHostWarningSilentOnError verifies that a `who` failure is silently skipped.
func TestSharedHostWarningSilentOnError(t *testing.T) {
	var buf bytes.Buffer
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	checkSharedHost(false, "", func() ([]string, error) {
		return nil, fmt.Errorf("who: not found")
	})

	w.Close()
	os.Stderr = orig
	io.Copy(&buf, r)

	if buf.Len() > 0 {
		t.Errorf("expected no warning on who error, got: %q", buf.String())
	}
}

// TestGatewayVMNotFound verifies 404 for an unknown VM name.
func TestGatewayVMNotFound(t *testing.T) {
	dir := t.TempDir()
	const tok = "tok"
	gw, err := NewGateway(dir, tok, false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/vms/nonexistent/status", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

// TestGatewayFSNotifyAddsRoute verifies that creating a listening socket file
// in a VM directory causes the gateway to hot-add the route via fsnotify.
// Uses /tmp directly to avoid Unix socket path length limits (104 bytes on darwin).
func TestGatewayFSNotifyAddsRoute(t *testing.T) {
	vmDir, err := os.MkdirTemp("", "gwfsnot*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(vmDir) })

	vmName := "vm"
	vmSubDir := filepath.Join(vmDir, vmName)
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}

	gw, err := NewGateway(vmDir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	// Start the watcher loop (no TCP listener needed for this test).
	_ = gw.watcher.Add(vmDir)
	go gw.watch()
	defer gw.Stop()

	// Initially no route.
	gw.mu.RLock()
	_, found := gw.routes[vmName]
	gw.mu.RUnlock()
	if found {
		t.Fatal("expected no route before socket exists")
	}

	// Create a listening socket — fsnotify Create event should add the route.
	sockPath := filepath.Join(vmSubDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Wait up to 2s for the route to appear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		gw.mu.RLock()
		_, ok := gw.routes[vmName]
		gw.mu.RUnlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	gw.mu.RLock()
	_, found = gw.routes[vmName]
	gw.mu.RUnlock()
	if !found {
		t.Error("route not added within 2s of socket creation")
	}
}

// TestGatewayFSNotifyRemovesRoute verifies that removing a socket file causes
// the gateway to drop the route via fsnotify.
func TestGatewayFSNotifyRemovesRoute(t *testing.T) {
	vmDir, err := os.MkdirTemp("", "gwfsrem*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(vmDir) })

	vmName := "vm"
	vmSubDir := filepath.Join(vmDir, vmName)
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Create the socket before building the gateway so it's in the initial scan.
	sockPath := filepath.Join(vmSubDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}

	gw, err := NewGateway(vmDir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	// Speed up the liveness ticker — on darwin, fsnotify does not deliver
	// REMOVE events for Unix sockets, so the liveness rescan is the only
	// mechanism that drops dead routes.
	gw.livenessInterval = 100 * time.Millisecond
	_ = gw.watcher.Add(vmDir)
	go gw.watch()
	defer gw.Stop()

	// Route should be present after initial refresh (socket is listening).
	gw.mu.RLock()
	_, found := gw.routes[vmName]
	gw.mu.RUnlock()
	if !found {
		t.Fatal("expected route after initial scan with listening socket")
	}

	// Close listener and remove the socket file. fsnotify may or may not
	// deliver a REMOVE event (it does not on darwin for sockets); the
	// liveness rescan must drop the route either way.
	ln.Close()
	os.Remove(sockPath)

	// Wait up to 2s for the route to disappear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		gw.mu.RLock()
		_, ok := gw.routes[vmName]
		gw.mu.RUnlock()
		if !ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	gw.mu.RLock()
	_, found = gw.routes[vmName]
	gw.mu.RUnlock()
	if found {
		t.Error("route not removed within 2s of socket removal")
	}
}

// TestGatewayAsyncSnapshotSave verifies POST /v1/vms/<name>/snapshot?async=true
// returns 202 + op-id immediately, then completes via the LRO once the slow
// save (simulated here with a 200ms delay) finishes. Acceptance #2 from the
// brief: HTTP path no longer blocks on the 30s proxy deadline for large saves.
func TestGatewayAsyncSnapshotSave(t *testing.T) {
	vmDir, err := os.MkdirTemp("", "gwasync*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(vmDir) })

	const (
		vmName   = "vm"
		token    = "master-token"
		snapName = "checkpoint1"
	)
	vmSubDir := filepath.Join(vmDir, vmName)
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(vmSubDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})

	// Fake control socket that takes 200ms to "save" then returns success.
	// Slow enough to observe running→succeeded transition without flakiness.
	const fakeSaveDelay = 200 * time.Millisecond
	gotReq := make(chan *controlpb.ControlRequest, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var got controlpb.ControlRequest
				if err := protojsonUnmarshaler.Unmarshal(scanner.Bytes(), &got); err != nil {
					return
				}
				gotReq <- &got
				time.Sleep(fakeSaveDelay)
				resp := &controlpb.ControlResponse{
					Success: true,
					Result: &controlpb.ControlResponse_SnapshotAction{
						SnapshotAction: &controlpb.SnapshotActionResponse{Message: "snapshot saved"},
					},
				}
				data, _ := protojsonMarshaler.Marshal(resp)
				fmt.Fprintf(conn, "%s\n", data)
			}(conn)
		}
	}()

	reg := newServeTestRegistry(t)
	gw, err := NewGateway(vmDir, token, false, nil, reg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.refresh()
	gw.mu.RLock()
	_, ok := gw.routes[vmName]
	gw.mu.RUnlock()
	if !ok {
		t.Fatal("route not registered after refresh")
	}

	body := bytes.NewBufferString(fmt.Sprintf(`{"name":%q}`, snapName))
	req := httptest.NewRequest(http.MethodPost, "/v1/vms/"+vmName+"/snapshot?async=true", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202; body=%q", rec.Code, rec.Body.String())
	}
	// Acceptance #1 (HTTP variant): return immediately, well under the fake save delay.
	if elapsed > fakeSaveDelay/2 {
		t.Errorf("async response took %v, expected <%v (the fake save itself takes %v)", elapsed, fakeSaveDelay/2, fakeSaveDelay)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/v1/operations/op_") {
		t.Fatalf("Location header: %q", loc)
	}
	var opResp operations.Operation
	if err := json.NewDecoder(rec.Body).Decode(&opResp); err != nil {
		t.Fatalf("decode 202 body: %v", err)
	}
	wantResource := fmt.Sprintf("vms/%s/snapshots/%s", vmName, snapName)
	if opResp.Resource != wantResource {
		t.Errorf("resource = %q, want %q", opResp.Resource, wantResource)
	}

	// Confirm the control socket received a snapshot save request.
	select {
	case got := <-gotReq:
		if got.Type != "snapshot" {
			t.Errorf("control request type = %q, want snapshot", got.Type)
		}
		snap := got.GetSnapshot()
		if snap == nil || snap.Action != "save" || snap.Name != snapName {
			t.Errorf("snapshot cmd = %+v", snap)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for control request")
	}

	// Poll for terminal success.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, ok := reg.Get(opResp.ID)
		if !ok {
			t.Fatalf("operation %s vanished", opResp.ID)
		}
		if op.Status == "succeeded" {
			if op.Result["snapshot"] != snapName || op.Result["vm"] != vmName {
				t.Errorf("result = %+v", op.Result)
			}
			return
		}
		if op.Status == "failed" {
			t.Fatalf("operation failed: %+v", op.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("operation did not reach succeeded within 2s")
}

// TestGatewayAsyncSnapshotSaveFailurePropagates verifies that when the
// underlying control socket reports an error, the LRO transitions to failed
// with the message preserved (acceptance #3 from the brief).
func TestGatewayAsyncSnapshotSaveFailurePropagates(t *testing.T) {
	vmDir, err := os.MkdirTemp("", "gwasyncfail*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(vmDir) })

	const (
		vmName   = "vm"
		token    = "master-token"
		snapName = "broken"
		errMsg   = "disk full"
	)
	vmSubDir := filepath.Join(vmDir, vmName)
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(vmSubDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				resp := &controlpb.ControlResponse{Error: errMsg}
				data, _ := protojsonMarshaler.Marshal(resp)
				fmt.Fprintf(conn, "%s\n", data)
			}(conn)
		}
	}()

	reg := newServeTestRegistry(t)
	gw, err := NewGateway(vmDir, token, false, nil, reg)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.refresh()

	body := bytes.NewBufferString(fmt.Sprintf(`{"name":%q}`, snapName))
	req := httptest.NewRequest(http.MethodPost, "/v1/vms/"+vmName+"/snapshot?async=true", body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}
	var opResp operations.Operation
	if err := json.NewDecoder(rec.Body).Decode(&opResp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, _ := reg.Get(opResp.ID)
		if op != nil && op.Status == "failed" {
			if op.Error == nil || op.Error.Message != errMsg || op.Error.Code != "snapshot_save" {
				t.Errorf("error = %+v, want code=snapshot_save message=%q", op.Error, errMsg)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("operation did not reach failed within 2s")
}

// TestGatewaySnapshotSaveSyncStillWorks verifies the existing synchronous
// path (no ?async) is unchanged — acceptance #4: scripts that don't opt in
// keep blocking-on-completion semantics.
func TestGatewaySnapshotSaveSyncStillWorks(t *testing.T) {
	vmDir, err := os.MkdirTemp("", "gwsyncsnap*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(vmDir) })

	const (
		vmName   = "vm"
		token    = "master-token"
		snapName = "syncsnap"
	)
	vmSubDir := filepath.Join(vmDir, vmName)
	if err := os.MkdirAll(vmSubDir, 0700); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(vmSubDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})
	gotReq := make(chan *controlpb.ControlRequest, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var got controlpb.ControlRequest
				if err := protojsonUnmarshaler.Unmarshal(scanner.Bytes(), &got); err == nil {
					gotReq <- &got
				}
				resp := &controlpb.ControlResponse{
					Success: true,
					Result: &controlpb.ControlResponse_SnapshotAction{
						SnapshotAction: &controlpb.SnapshotActionResponse{Message: "ok"},
					},
				}
				data, _ := protojsonMarshaler.Marshal(resp)
				fmt.Fprintf(conn, "%s\n", data)
			}(conn)
		}
	}()

	gw, err := NewGateway(vmDir, token, false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.refresh()

	body := bytes.NewBufferString(fmt.Sprintf(`{"name":%q}`, snapName))
	req := httptest.NewRequest(http.MethodPost, "/v1/vms/"+vmName+"/snapshot", body)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("sync save: got %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	select {
	case got := <-gotReq:
		snap := got.GetSnapshot()
		if snap == nil || snap.Action != "save" || snap.Name != snapName {
			t.Errorf("snapshot cmd = %+v", snap)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for control request")
	}
}
