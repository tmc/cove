package coved

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/fleet/fleetproto"
	"github.com/tmc/cove/internal/vmpolicy"
)

// TestBoundedHandlerAppliesPolicy writes a policy to two local VM dirs and
// confirms the handler reports an applied count of 2 and that the policy lands
// on disk in the vmpolicy format.
func TestBoundedHandlerAppliesPolicy(t *testing.T) {
	vmRoot := t.TempDir()
	for _, name := range []string{"vm-a", "vm-b"} {
		if err := os.MkdirAll(filepath.Join(vmRoot, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	// A stray file at the root must be ignored (only directories are VMs).
	if err := os.WriteFile(filepath.Join(vmRoot, "README"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	h := &BoundedHandler{VMRoot: vmRoot}
	payload, err := json.Marshal(fleetproto.PolicyPayload{IdleTimeout: "30m", MaxAge: "24h", RunBudget: 9})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	state, detail, err := h.Handle(context.Background(), fleetproto.Assignment{Kind: fleetproto.KindPolicy, Payload: payload})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if state != fleetproto.StateDone {
		t.Fatalf("state = %q, want done", state)
	}
	var res fleetproto.PolicyResult
	if err := json.Unmarshal([]byte(detail), &res); err != nil {
		t.Fatalf("decode detail %q: %v", detail, err)
	}
	if res.Applied != 2 || res.Failed != 0 {
		t.Fatalf("result = %+v, want applied 2 / failed 0", res)
	}

	// The policy must be readable back via vmpolicy on each VM.
	for _, name := range []string{"vm-a", "vm-b"} {
		p, err := vmpolicy.Load(filepath.Join(vmRoot, name))
		if err != nil {
			t.Fatalf("load %s policy: %v", name, err)
		}
		if p.RunBudget != 9 || p.IdleTimeout.Minutes() != 30 || p.MaxAge.Hours() != 24 {
			t.Fatalf("%s policy = %+v, want 30m/24h/9", name, p)
		}
	}
}

func TestBoundedHandlerPolicyEmptyRoot(t *testing.T) {
	h := &BoundedHandler{VMRoot: filepath.Join(t.TempDir(), "absent")}
	state, detail, err := h.Handle(context.Background(), fleetproto.Assignment{Kind: fleetproto.KindPolicy})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if state != fleetproto.StateDone {
		t.Fatalf("state = %q, want done", state)
	}
	var res fleetproto.PolicyResult
	if err := json.Unmarshal([]byte(detail), &res); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if res.Applied != 0 {
		t.Fatalf("applied = %d, want 0 for empty root", res.Applied)
	}
}

func TestBoundedHandlerPolicyRejectsBadDuration(t *testing.T) {
	h := &BoundedHandler{VMRoot: t.TempDir()}
	payload := json.RawMessage(`{"idle_timeout":"nonsense"}`)
	state, _, err := h.Handle(context.Background(), fleetproto.Assignment{Kind: fleetproto.KindPolicy, Payload: payload})
	if err == nil {
		t.Fatal("expected bad duration to fail")
	}
	if state != fleetproto.StateFailed {
		t.Fatalf("state = %q, want failed", state)
	}
}

// TestBoundedHandlerRunsImageGC points the handler at an isolated home with one
// unreferenced image and confirms the assignment reports it removed.
func TestBoundedHandlerRunsImageGC(t *testing.T) {
	home := t.TempDir()
	imageDir := filepath.Join(home, ".vz", "images", "base", "latest")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("mkdir image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "manifest.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "disk.img"), make([]byte, 2048), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	// No VMs reference the image, so GC must remove it. Use an isolated metrics
	// path so the run does not touch the developer's ~/.vz.
	gc := NewImageGCScheduler(home, nil)
	gc.MetricsPath = filepath.Join(home, "metrics.jsonl")
	h := &BoundedHandler{HomeDir: home, ImageGC: gc}

	state, detail, err := h.Handle(context.Background(), fleetproto.Assignment{Kind: fleetproto.KindImageGC})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if state != fleetproto.StateDone {
		t.Fatalf("state = %q, want done", state)
	}
	var res fleetproto.ImageGCResult
	if err := json.Unmarshal([]byte(detail), &res); err != nil {
		t.Fatalf("decode detail %q: %v", detail, err)
	}
	if res.ManifestsScanned != 1 || res.ManifestsRemoved != 1 {
		t.Fatalf("result = %+v, want scanned 1 / removed 1", res)
	}
	if _, err := os.Stat(imageDir); !os.IsNotExist(err) {
		t.Fatalf("image dir still exists after gc: %v", err)
	}
}
