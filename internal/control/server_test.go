package control

import (
	"bufio"
	"net"
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
