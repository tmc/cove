package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestGatewayHandleFSEventCreateAddsVMDirWatch(t *testing.T) {
	dir := t.TempDir()
	gw, err := NewGateway(dir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	vmSubdir := filepath.Join(dir, "fresh-vm")
	if err := os.MkdirAll(vmSubdir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	gw.handleFSEvent(fsnotify.Event{
		Name: vmSubdir,
		Op:   fsnotify.Create,
	})

	gw.mu.RLock()
	_, ok := gw.routes["fresh-vm"]
	gw.mu.RUnlock()
	if ok {
		t.Error("route should not be added when control.sock is absent")
	}
}

func TestGatewayHandleFSEventIgnoresNonControlSock(t *testing.T) {
	dir := t.TempDir()
	gw, err := NewGateway(dir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.routes["foo"] = &vmRoute{name: "foo", socketPath: filepath.Join(dir, "foo", "control.sock")}

	gw.handleFSEvent(fsnotify.Event{
		Name: filepath.Join(dir, "foo", "metadata.json"),
		Op:   fsnotify.Write,
	})

	gw.mu.RLock()
	_, ok := gw.routes["foo"]
	gw.mu.RUnlock()
	if !ok {
		t.Error("non-control.sock event should not delete route")
	}
}

func TestGatewayHandleFSEventRemovesRouteOnSockRemove(t *testing.T) {
	dir := t.TempDir()
	gw, err := NewGateway(dir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.routes["bar"] = &vmRoute{name: "bar", socketPath: filepath.Join(dir, "bar", "control.sock")}

	gw.handleFSEvent(fsnotify.Event{
		Name: filepath.Join(dir, "bar", "control.sock"),
		Op:   fsnotify.Remove,
	})

	gw.mu.RLock()
	_, ok := gw.routes["bar"]
	gw.mu.RUnlock()
	if ok {
		t.Error("Remove event on control.sock should delete route")
	}
}

func TestGatewayHandleFSEventRenameRemovesRoute(t *testing.T) {
	dir := t.TempDir()
	gw, err := NewGateway(dir, "tok", false, nil, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.routes["baz"] = &vmRoute{name: "baz", socketPath: filepath.Join(dir, "baz", "control.sock")}

	gw.handleFSEvent(fsnotify.Event{
		Name: filepath.Join(dir, "baz", "control.sock"),
		Op:   fsnotify.Rename,
	})

	gw.mu.RLock()
	_, ok := gw.routes["baz"]
	gw.mu.RUnlock()
	if ok {
		t.Error("Rename event on control.sock should delete route")
	}
}

func TestGatewayHandleFSEventAllowlistFiltersRemove(t *testing.T) {
	dir := t.TempDir()
	gw, err := NewGateway(dir, "tok", false, []string{"allowed"}, newServeTestRegistry(t))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	gw.routes["blocked"] = &vmRoute{name: "blocked", socketPath: filepath.Join(dir, "blocked", "control.sock")}

	// A Remove on a VM not in the allowlist must be ignored — the route stays.
	gw.handleFSEvent(fsnotify.Event{
		Name: filepath.Join(dir, "blocked", "control.sock"),
		Op:   fsnotify.Remove,
	})

	gw.mu.RLock()
	_, ok := gw.routes["blocked"]
	gw.mu.RUnlock()
	if !ok {
		t.Error("Remove for VM outside allowlist should not delete the route")
	}
}
