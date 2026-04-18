package main

import (
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
)

// newServeTestRegistry returns an in-memory OperationRegistry for testing.
func newServeTestRegistry(t *testing.T) *OperationRegistry {
	t.Helper()
	reg, err := NewOperationRegistry(NewMemOperationStore())
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

	var opResp Operation
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
	var got Operation
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
