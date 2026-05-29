package fleet

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeDaemon is a minimal unix-socket server speaking the newline-JSON command
// protocol the LocalProvider expects, for testing the MIT local path without the
// real coved daemon.
type fakeDaemon struct {
	mu   sync.Mutex
	cmds []localCommand
}

func startFakeDaemon(t *testing.T) (*fakeDaemon, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "cove.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := &fakeDaemon{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.handle(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return d, sock
}

func (d *fakeDaemon) handle(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	var cmd localCommand
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		_, _ = conn.Write([]byte(`{"error":"bad command"}` + "\n"))
		return
	}
	d.mu.Lock()
	d.cmds = append(d.cmds, cmd)
	d.mu.Unlock()

	var resp localResponse
	switch cmd.Op {
	case "create":
		resp.Sandbox = Sandbox{ID: "local-1", State: SandboxRunning, Host: "localhost"}
	case "get":
		resp.Sandbox = Sandbox{ID: cmd.ID, State: SandboxRunning, Host: "localhost"}
	case "start":
		resp.Sandbox = Sandbox{ID: cmd.ID, State: SandboxRunning}
	case "stop":
		resp.Sandbox = Sandbox{ID: cmd.ID, State: SandboxStopped}
	case "delete":
		resp.Sandbox = Sandbox{ID: cmd.ID, State: SandboxDeleted}
	default:
		resp.Error = "unknown op"
	}
	out, _ := json.Marshal(resp)
	_, _ = conn.Write(append(out, '\n'))
}

func (d *fakeDaemon) ops() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.cmds))
	for i, c := range d.cmds {
		out[i] = c.Op
	}
	return out
}

func TestLocalProviderLifecycle(t *testing.T) {
	d, sock := startFakeDaemon(t)
	p := &LocalProvider{Socket: sock, Timeout: 2 * time.Second}
	ctx := context.Background()

	sb, err := p.Create(ctx, CreateRequest{BaseRef: "base:latest", Name: "x", RAMBytes: 1 << 30})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.ID != "local-1" || sb.State != SandboxRunning {
		t.Errorf("create = %+v, want id local-1 running", sb)
	}
	if _, err := p.Get(ctx, sb.ID); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := p.Start(ctx, sb.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got, err := p.Stop(ctx, sb.ID); err != nil || got.State != SandboxStopped {
		t.Fatalf("stop = %+v, err %v", got, err)
	}
	if err := p.Delete(ctx, sb.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	want := []string{"create", "get", "start", "stop", "delete"}
	got := d.ops()
	if len(got) != len(want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ops[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLocalProviderCreateRequiresBaseRef(t *testing.T) {
	p := &LocalProvider{Socket: filepath.Join(t.TempDir(), "nope.sock")}
	if _, err := p.Create(context.Background(), CreateRequest{}); err == nil {
		t.Errorf("create without base ref did not error")
	}
}

func TestLocalProviderDialError(t *testing.T) {
	p := &LocalProvider{Socket: filepath.Join(t.TempDir(), "absent.sock"), Timeout: time.Second}
	if _, err := p.Get(context.Background(), "x"); err == nil {
		t.Errorf("get against missing socket did not error")
	}
}

// TestCloudProviderAgainstHostedAPI proves the same Provider surface works over
// the hosted REST API: a CloudProvider drives create/get/start/stop/delete
// against a live HostedAPI, and the host stays hidden from the caller.
func TestCloudProviderAgainstHostedAPI(t *testing.T) {
	sched := &fakeScheduler{host: "secret-host"}
	api := NewHostedAPI(sched, NewSandboxStore(), NewUsageLedger(), nil, []string{"key"})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)

	p, err := NewCloudProvider(srv.URL, "key", srv.Client())
	if err != nil {
		t.Fatalf("new cloud provider: %v", err)
	}
	ctx := context.Background()

	sb, err := p.Create(ctx, CreateRequest{BaseRef: "base:latest"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.ID == "" || sb.State != SandboxRunning {
		t.Errorf("create = %+v, want running with id", sb)
	}
	if sb.Host != "" {
		t.Errorf("create leaked host %q over cloud provider", sb.Host)
	}

	if got, err := p.Get(ctx, sb.ID); err != nil || got.Host != "" {
		t.Errorf("get = %+v, err %v; host must stay hidden", got, err)
	}
	if got, err := p.Wait(ctx, sb.ID, time.Second); err != nil || got.State != SandboxRunning {
		t.Errorf("wait = %+v, err %v", got, err)
	}
	if got, err := p.Stop(ctx, sb.ID); err != nil || got.State != SandboxStopped {
		t.Errorf("stop = %+v, err %v", got, err)
	}
	if got, err := p.Start(ctx, sb.ID); err != nil || got.State != SandboxRunning {
		t.Errorf("start = %+v, err %v", got, err)
	}
	if err := p.Delete(ctx, sb.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
}

func TestCloudProviderAuthRejected(t *testing.T) {
	api := NewHostedAPI(&fakeScheduler{}, NewSandboxStore(), NewUsageLedger(), nil, []string{"good"})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)

	p, err := NewCloudProvider(srv.URL, "wrong", srv.Client())
	if err != nil {
		t.Fatalf("new cloud provider: %v", err)
	}
	if _, err := p.Create(context.Background(), CreateRequest{BaseRef: "base:latest"}); err == nil {
		t.Errorf("create with wrong api key did not error")
	}
}

func TestNewCloudProviderRequiresURL(t *testing.T) {
	if _, err := NewCloudProvider("", "key", nil); err == nil {
		t.Errorf("NewCloudProvider with empty url did not error")
	}
}
