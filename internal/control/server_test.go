package control

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestPopulateLegacyRequestPayloads(t *testing.T) {
	req := &controlpb.ControlRequest{Type: "agent-exec"}
	PopulateLegacyRequestPayloads(`{"type":"agent-exec","token":"tok","args":["echo","ok"],"working_dir":"/tmp","env":{"A":"B"}}`, req)

	if req.AuthToken != "tok" {
		t.Fatalf("AuthToken = %q, want tok", req.AuthToken)
	}
	cmd := req.GetAgentExec()
	if cmd == nil {
		t.Fatal("AgentExec payload missing")
	}
	if got := cmd.Args; len(got) != 2 || got[0] != "echo" || got[1] != "ok" {
		t.Fatalf("Args = %#v", got)
	}
	if cmd.WorkingDir != "/tmp" || cmd.Env["A"] != "B" {
		t.Fatalf("payload = %#v", cmd)
	}
}

func TestServeConnectionDispatchesAuthorizedRequest(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	h := &fakeHandler{token: "secret"}
	done := make(chan struct{})
	go func() {
		ServeConnection(server, h)
		close(done)
	}()

	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte(`{"type":"ping","token":"secret"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line == "" {
		t.Fatal("empty response")
	}
	client.Close()
	<-done
	if h.handled != "ping" || h.events != 1 {
		t.Fatalf("handled=%q events=%d", h.handled, h.events)
	}
}

func TestServerStopDoesNotWaitForeverForHealthMonitor(t *testing.T) {
	sock := shortUnixSocketPath(t)
	healthDone := make(chan struct{})
	s := &Server{
		SocketPath:  sock,
		Handler:     &fakeHandler{token: "secret"},
		StopTimeout: 20 * time.Millisecond,
		HealthMonitor: func() {
			<-healthDone
		},
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer close(healthDone)

	start := time.Now()
	s.Stop()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Stop took %v, want bounded return", elapsed)
	}
}

func TestServerStopClosesActiveConnections(t *testing.T) {
	sock := shortUnixSocketPath(t)
	s := &Server{
		SocketPath: sock,
		Handler:    &fakeHandler{token: "secret"},
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := conn.Write([]byte(`{"type":"ping","token":"secret"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		t.Fatalf("read response: %v", err)
	}

	s.Stop()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := bufio.NewReader(conn).ReadString('\n'); err == nil {
		t.Fatal("read after Stop succeeded, want closed connection")
	}
	_ = conn.Close()
}

type fakeHandler struct {
	token   string
	handled string
	events  int
}

func (h *fakeHandler) Authorize(token string) bool {
	return token == h.token
}

func (h *fakeHandler) HandleStream(net.Conn, *controlpb.ControlRequest, []byte) (bool, bool) {
	return false, false
}

func (h *fakeHandler) HandleRaw(*controlpb.ControlRequest, []byte) (*controlpb.ControlResponse, bool) {
	return nil, false
}

func (h *fakeHandler) Handle(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	h.handled = req.Type
	return &controlpb.ControlResponse{Success: true, Data: "ok"}
}

func (h *fakeHandler) Event(string, *controlpb.ControlResponse) {
	h.events++
}

func shortUnixSocketPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("/tmp", "cove-control-test-"+time.Now().Format("150405.000000000"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "control.sock")
}
