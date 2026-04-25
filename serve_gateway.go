package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tmc/vz-macos/internal/control/operations"
	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// Gateway is a multi-VM HTTP gateway that proxies requests to individual VM
// control sockets. It discovers VMs via fsnotify on ~/.vz/vms/ and
// hot-adds/removes routes as VMs start and stop. A 30s liveness ticker
// re-checks reachability (fsnotify detects file create/remove but not whether
// a socket stopped accepting connections).
type Gateway struct {
	vmDir       string
	masterToken string
	perVMAuth   bool
	allowlist   map[string]bool // empty = allow all
	ops         *operations.OperationRegistry

	mu      sync.RWMutex
	routes  map[string]*vmRoute // name -> route
	watcher *fsnotify.Watcher
	stop    chan struct{}

	// livenessInterval controls how often the watcher rescans VM sockets
	// to catch removals that fsnotify misses (e.g. Unix sockets on darwin).
	// Tests may override before Start; production defaults to 30s.
	livenessInterval time.Duration
}

// vmRoute holds per-VM routing state.
type vmRoute struct {
	name       string
	socketPath string
	perVMToken string // from ~/.vz/vms/<name>/control.token
}

// NewGateway builds a gateway for the VMs rooted at vmDir. It performs an
// initial route enumeration immediately. The fsnotify watcher and liveness
// ticker start on Start.
func NewGateway(vmDir, masterToken string, perVMAuth bool, allowlist []string, ops *operations.OperationRegistry) (*Gateway, error) {
	al := make(map[string]bool, len(allowlist))
	for _, name := range allowlist {
		al[name] = true
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	// Best-effort: watch the vmDir. If it doesn't exist yet, Start will retry.
	_ = watcher.Add(vmDir)

	g := &Gateway{
		vmDir:       vmDir,
		masterToken: masterToken,
		perVMAuth:   perVMAuth,
		allowlist:   al,
		ops:         ops,
		routes:      make(map[string]*vmRoute),
		watcher:     watcher,
		stop:        make(chan struct{}),
	}
	// Watch vmDir itself (for new VM subdirs being created) plus all existing subdirs.
	_ = watcher.Add(vmDir)
	g.initialScan()
	return g, nil
}

// Start binds the HTTP listener and starts the fsnotify watch loop. The caller
// should call http.Serve(ln, g) to begin serving requests.
func (g *Gateway) Start(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", addr, err)
	}
	// Ensure vmDir is watched (it may have been created after NewGateway).
	_ = g.watcher.Add(g.vmDir)
	go g.watch()
	return ln, nil
}

// Stop shuts down the fsnotify watcher and watch goroutine.
func (g *Gateway) Stop() {
	select {
	case <-g.stop:
	default:
		close(g.stop)
	}
	g.watcher.Close()
}

// watch drives route hot-add/remove via fsnotify events, supplemented by a
// 30s liveness ticker that re-checks socket reachability (fsnotify detects
// file creation/removal but not whether a listening socket stopped accepting).
func (g *Gateway) watch() {
	interval := g.livenessInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-g.watcher.Events:
			if !ok {
				return
			}
			g.handleFSEvent(event)
		case _, ok := <-g.watcher.Errors:
			if !ok {
				return
			}
			// Watcher errors are non-fatal; liveness ticker keeps us consistent.
		case <-ticker.C:
			g.checkLiveness()
		case <-g.stop:
			return
		}
	}
}

// handleFSEvent reacts to a single fsnotify event.
func (g *Gateway) handleFSEvent(event fsnotify.Event) {
	base := filepath.Base(event.Name)

	// A new directory appearing directly under vmDir is a new VM dir.
	// Add a watch so we receive control.sock events inside it.
	if event.Has(fsnotify.Create) && filepath.Dir(event.Name) == g.vmDir {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			_ = g.watcher.Add(event.Name)
			// Check if control.sock already exists inside (race: created before watch).
			sockPath := filepath.Join(event.Name, "control.sock")
			if socketIsReachable(sockPath) {
				vmName := base
				if len(g.allowlist) > 0 && !g.allowlist[vmName] {
					return
				}
				tokenPath := filepath.Join(g.vmDir, vmName, controlTokenFileName)
				token, _ := LoadControlTokenFromPath(tokenPath)
				g.mu.Lock()
				g.routes[vmName] = &vmRoute{name: vmName, socketPath: sockPath, perVMToken: token}
				g.mu.Unlock()
			}
			return
		}
	}

	// We only care about events on files named "control.sock".
	if base != "control.sock" {
		return
	}
	vmName := filepath.Base(filepath.Dir(event.Name))
	if len(g.allowlist) > 0 && !g.allowlist[vmName] {
		return
	}

	switch {
	case event.Has(fsnotify.Create) || event.Has(fsnotify.Write):
		// Socket created or replaced — dial to confirm it's listening.
		if socketIsReachable(event.Name) {
			tokenPath := filepath.Join(g.vmDir, vmName, controlTokenFileName)
			token, _ := LoadControlTokenFromPath(tokenPath)
			g.mu.Lock()
			g.routes[vmName] = &vmRoute{
				name:       vmName,
				socketPath: event.Name,
				perVMToken: token,
			}
			g.mu.Unlock()
		}
	case event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename):
		g.mu.Lock()
		delete(g.routes, vmName)
		g.mu.Unlock()
	}
}

// initialScan scans vmDir for control sockets, dials each (200ms timeout), and
// populates the route table. Called once at construction.
func (g *Gateway) initialScan() {
	entries, err := os.ReadDir(g.vmDir)
	if err != nil {
		return // vmDir may not exist yet; skip silently
	}

	reachable := make(map[string]*vmRoute)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(g.allowlist) > 0 && !g.allowlist[name] {
			continue
		}
		// Watch every VM subdirectory for control.sock create/remove events,
		// even if the socket isn't present yet — otherwise a VM that starts
		// later will be invisible until the next liveness tick.
		_ = g.watcher.Add(filepath.Join(g.vmDir, name))
		sockPath := filepath.Join(g.vmDir, name, "control.sock")
		if !socketIsReachable(sockPath) {
			continue
		}
		tokenPath := filepath.Join(g.vmDir, name, controlTokenFileName)
		token, _ := LoadControlTokenFromPath(tokenPath) // empty on error
		reachable[name] = &vmRoute{
			name:       name,
			socketPath: sockPath,
			perVMToken: token,
		}
	}

	g.mu.Lock()
	g.routes = reachable
	g.mu.Unlock()
}

// checkLiveness dials all currently-known routes and drops any that are
// no longer accepting connections. This is the 30s belt-and-suspenders check
// for VMs that died without removing their socket file (fsnotify won't fire).
func (g *Gateway) checkLiveness() {
	g.mu.RLock()
	names := make([]string, 0, len(g.routes))
	for name := range g.routes {
		names = append(names, name)
	}
	g.mu.RUnlock()

	for _, name := range names {
		g.mu.RLock()
		route, ok := g.routes[name]
		g.mu.RUnlock()
		if !ok {
			continue
		}
		if !socketIsReachable(route.socketPath) {
			g.mu.Lock()
			delete(g.routes, name)
			g.mu.Unlock()
		}
	}
}

// refresh is kept for test compatibility; it delegates to initialScan.
func (g *Gateway) refresh() { g.initialScan() }

// socketIsReachable dials a Unix socket with a 200ms timeout.
func socketIsReachable(path string) bool {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ServeHTTP implements http.Handler for the gateway.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// No-auth health endpoint.
	if path == "/healthz" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}` + "\n"))
		return
	}

	// List VMs — master auth.
	if path == "/v1/vms" && r.Method == http.MethodGet {
		if !g.authorizeMaster(w, r) {
			return
		}
		g.handleListVMs(w, r)
		return
	}

	// Create VM — master auth, async LRO.
	if path == "/v1/vms" && r.Method == http.MethodPost {
		if !g.authorizeMaster(w, r) {
			return
		}
		g.handleCreateVM(w, r)
		return
	}

	// Operations routes — master auth.
	if strings.HasPrefix(path, "/v1/operations") {
		if !g.authorizeMaster(w, r) {
			return
		}
		g.handleOperations(w, r)
		return
	}

	// Per-VM proxy: /v1/vms/<name>/...
	const vmPrefix = "/v1/vms/"
	if strings.HasPrefix(path, vmPrefix) {
		rest := strings.TrimPrefix(path, vmPrefix)
		slash := strings.Index(rest, "/")
		var name string
		if slash < 0 {
			name = rest
		} else {
			name = rest[:slash]
		}
		if name == "" {
			http.Error(w, "missing vm name", http.StatusBadRequest)
			return
		}
		g.mu.RLock()
		route, ok := g.routes[name]
		g.mu.RUnlock()
		if !ok {
			http.Error(w, fmt.Sprintf("vm %q not found or not running", name), http.StatusNotFound)
			return
		}
		if g.perVMAuth {
			if !g.authorizePerVM(w, r, route.perVMToken) {
				return
			}
		} else {
			if !g.authorizeMaster(w, r) {
				return
			}
		}
		g.proxyToSocket(route, w, r)
		return
	}

	http.NotFound(w, r)
}

func (g *Gateway) authorizeMaster(w http.ResponseWriter, r *http.Request) bool {
	token := bearerToken(r)
	if token == "" || token != g.masterToken {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cove-gateway"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (g *Gateway) authorizePerVM(w http.ResponseWriter, r *http.Request, perVMToken string) bool {
	token := bearerToken(r)
	if token == "" || token != perVMToken {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cove-vm"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}

func (g *Gateway) handleListVMs(w http.ResponseWriter, _ *http.Request) {
	names := g.discoverConfiguredVMs()

	type vmEntry struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	vms := make([]vmEntry, 0, len(names))
	for _, name := range names {
		vms = append(vms, vmEntry{Name: name, Status: "running"})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"vms": vms})
}

// discoverConfiguredVMs returns the names of all VMs visible to the gateway,
// scanning both the canonical g.vmDir layout (~/.vz/vms/<name>/) and the
// legacy peer layout (~/.vz/<name>/) used by older builds. Names from the
// canonical layout win when both exist. Allowlist gating is honored.
func (g *Gateway) discoverConfiguredVMs() []string {
	seen := make(map[string]bool)
	var names []string

	add := func(name string) {
		if seen[name] {
			return
		}
		if len(g.allowlist) > 0 && !g.allowlist[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}

	if entries, err := os.ReadDir(g.vmDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if !vmconfig.Validate(filepath.Join(g.vmDir, entry.Name())) {
				continue
			}
			add(entry.Name())
		}
	}

	legacyRoot := filepath.Dir(g.vmDir)
	canonicalBase := filepath.Base(g.vmDir)
	if entries, err := os.ReadDir(legacyRoot); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if name == canonicalBase {
				continue
			}
			if !vmconfig.Validate(filepath.Join(legacyRoot, name)) {
				continue
			}
			add(name)
		}
	}

	sort.Strings(names)
	return names
}

// handleCreateVM accepts POST /v1/vms, creates an LRO, and immediately marks
// it failed with code "not_implemented". This proves LRO plumbing end-to-end.
// TODO: wire actual create_vm logic here when the installer API is ready.
func (g *Gateway) handleCreateVM(w http.ResponseWriter, _ *http.Request) {
	if g.ops == nil {
		http.Error(w, "operations registry not available", http.StatusInternalServerError)
		return
	}
	op, err := g.ops.Create("vms/")
	if err != nil {
		http.Error(w, fmt.Sprintf("create operation: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Location", "/v1/operations/"+op.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(op)

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = g.ops.Fail(
			op.ID,
			"not_implemented",
			"create_vm via HTTP API is deferred to v0.2; use 'cove install' from the CLI (see docs/designs/001a-defer-create-vm-to-v02.md)",
		)
	}()
}

func (g *Gateway) handleOperations(w http.ResponseWriter, r *http.Request) {
	if g.ops == nil {
		http.Error(w, "operations registry not available", http.StatusInternalServerError)
		return
	}

	path := r.URL.Path
	const prefix = "/v1/operations"
	rest := strings.TrimPrefix(path, prefix)

	switch {
	case rest == "" || rest == "/":
		ops := g.ops.List()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"operations": ops})

	case strings.HasSuffix(rest, "/events"):
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/events")
		g.handleOpEvents(w, r, id)

	default:
		id := strings.Trim(rest, "/")
		op, ok := g.ops.Get(id)
		if !ok {
			http.Error(w, "operation not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(op)
	}
}

func (g *Gateway) handleOpEvents(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ch, err := g.ops.Subscribe(r.Context(), id)
	if err != nil {
		http.Error(w, "operation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for op := range ch {
		data, _ := json.Marshal(op)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

// proxyToSocket translates an HTTP request to a ControlRequest JSON line,
// dials the VM's Unix socket, and writes the response back.
func (g *Gateway) proxyToSocket(route *vmRoute, w http.ResponseWriter, r *http.Request) {
	cmdType, payload, err := httpPathToControlType(route.name, w, r)
	if err != nil {
		return // error already written
	}

	req := &controlpb.ControlRequest{
		Type:      cmdType,
		AuthToken: route.perVMToken,
	}
	if len(payload) > 0 {
		if err := mergeControlPayload(req, payload); err != nil {
			http.Error(w, fmt.Sprintf("build request: %v", err), http.StatusInternalServerError)
			return
		}
	}

	reqBytes, err := protojsonMarshaler.Marshal(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("marshal request: %v", err), http.StatusInternalServerError)
		return
	}

	conn, err := net.DialTimeout("unix", route.socketPath, 300*time.Millisecond)
	if err != nil {
		http.Error(w, fmt.Sprintf("connect to vm %q: %v", route.name, err), http.StatusBadGateway)
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	if _, err := fmt.Fprintf(conn, "%s\n", reqBytes); err != nil {
		http.Error(w, fmt.Sprintf("send request: %v", err), http.StatusBadGateway)
		return
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			http.Error(w, fmt.Sprintf("read response: %v", scanErr), http.StatusBadGateway)
		} else {
			http.Error(w, "vm closed connection without response", http.StatusBadGateway)
		}
		return
	}

	var resp controlpb.ControlResponse
	if err := protojsonUnmarshaler.Unmarshal(scanner.Bytes(), &resp); err != nil {
		http.Error(w, fmt.Sprintf("parse response: %v", err), http.StatusBadGateway)
		return
	}

	if resp.Error != "" {
		code := http.StatusInternalServerError
		if resp.Error == "unauthorized" {
			code = http.StatusUnauthorized
		}
		http.Error(w, resp.Error, code)
		return
	}

	if cmdType == "screenshot" {
		if sr := resp.GetScreenshotResult(); sr != nil && len(sr.GetImageData()) > 0 {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			w.Write(sr.GetImageData())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	data, _ := protojsonMarshaler.Marshal(&resp)
	w.Write(data)
	w.Write([]byte("\n"))
}

// mergeControlPayload merges JSON payload fields into req by round-tripping
// through JSON (proto-safe merge without reflection on private fields).
func mergeControlPayload(req *controlpb.ControlRequest, payload map[string]any) error {
	base, err := protojsonMarshaler.Marshal(req)
	if err != nil {
		return err
	}
	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return err
	}
	for k, v := range payload {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return protojsonUnmarshaler.Unmarshal(out, req)
}

// httpPathToControlType maps HTTP method + path to a ControlRequest type and
// optional JSON payload. Writes an HTTP error and returns an error if not found.
func httpPathToControlType(vmName string, w http.ResponseWriter, r *http.Request) (string, map[string]any, error) {
	const vmPrefix = "/v1/vms/"
	rest := strings.TrimPrefix(r.URL.Path, vmPrefix+vmName)

	var body map[string]any
	if r.Body != nil && r.Method != http.MethodGet {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	switch {
	case rest == "/status" && r.Method == http.MethodGet:
		return "status", nil, nil
	case rest == "/screenshot" && r.Method == http.MethodGet:
		return "screenshot", nil, nil
	case rest == "/pause" && r.Method == http.MethodPost:
		return "pause", nil, nil
	case rest == "/resume" && r.Method == http.MethodPost:
		return "resume", nil, nil
	case rest == "/stop" && r.Method == http.MethodPost:
		return "stop", body, nil
	case rest == "/type" && r.Method == http.MethodPost:
		return "type", body, nil
	case rest == "/key" && r.Method == http.MethodPost:
		return "key", body, nil
	case rest == "/mouse" && r.Method == http.MethodPost:
		return "mouse", body, nil
	case rest == "/agent/exec" && r.Method == http.MethodPost:
		return "agent-exec", body, nil
	case rest == "/agent/read" && r.Method == http.MethodGet:
		return "agent-read", map[string]any{"path": r.URL.Query().Get("path")}, nil
	case rest == "/agent/write" && r.Method == http.MethodPost:
		return "agent-write", body, nil
	case rest == "/agent/cp" && r.Method == http.MethodPost:
		return "agent-cp", body, nil
	case rest == "/snapshot" && r.Method == http.MethodPost:
		return "snapshot", snapshotControlPayload("save", snapshotNameFromBody(body)), nil
	case rest == "/snapshots" && r.Method == http.MethodGet:
		return "snapshot", snapshotControlPayload("list", ""), nil
	}

	if strings.HasPrefix(rest, "/snapshots/") {
		parts := strings.Split(strings.TrimPrefix(rest, "/snapshots/"), "/")
		if len(parts) == 1 && r.Method == http.MethodDelete {
			return "snapshot", snapshotControlPayload("delete", parts[0]), nil
		}
		if len(parts) == 2 && parts[1] == "restore" && r.Method == http.MethodPost {
			return "snapshot", snapshotControlPayload("restore", parts[0]), nil
		}
	}

	err := fmt.Errorf("unhandled: %s %s", r.Method, r.URL.Path)
	http.Error(w, err.Error(), http.StatusNotFound)
	return "", nil, err
}

func snapshotControlPayload(action, name string) map[string]any {
	payload := map[string]any{"action": action}
	if name != "" {
		payload["name"] = name
	}
	return map[string]any{"snapshot": payload}
}

func snapshotNameFromBody(body map[string]any) string {
	name, _ := body["name"].(string)
	return name
}
