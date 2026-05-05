package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	pb "github.com/tmc/vz-macos/proto/agentpb"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// fakeAttachAgent satisfies the attachAgent interface with canned behaviour
// so the Slice 1 attach handlers can be exercised without a live VM or
// connect-go server.
type fakeAttachAgent struct {
	mu        sync.Mutex
	streams   chan *fakeExecStream
	attach    bool
	resize    func(execID string, rows, cols uint32) error
	signal    func(execID string, sig int32) error
	openErr   error
	resizeLog []resizeCall
	signalLog []signalCall
}

type resizeCall struct {
	execID     string
	rows, cols uint32
}

type signalCall struct {
	execID string
	sig    int32
}

func (f *fakeAttachAgent) ExecAttachSupported(ctx context.Context) (bool, error) {
	return f.attach, nil
}

func (f *fakeAttachAgent) ExecAttachControl(ctx context.Context, execID string, tty bool, user string, args []string, env map[string]string, workDir string) (agentstate.ExecAttachStream, error) {
	return f.openStream(ctx, execID, tty, user, args, env, workDir)
}

func (f *fakeAttachAgent) ExecStreamControl(ctx context.Context, execID string, tty bool, user string, args []string, env map[string]string, workDir string) (agentstate.ExecStreamReceiver, error) {
	return f.openStream(ctx, execID, tty, user, args, env, workDir)
}

func (f *fakeAttachAgent) openStream(ctx context.Context, execID string, tty bool, user string, args []string, env map[string]string, workDir string) (*fakeExecStream, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	if !tty {
		return nil, errors.New("expected tty=true")
	}
	stream := &fakeExecStream{frames: make(chan *pb.ExecOutput, 4)}
	if args[0] == "echo" && len(args) > 1 {
		// Synthesize stdout from echo args + newline, then exit 0.
		stream.queue(&pb.ExecOutput{Stream: 0, Data: []byte(strings.Join(args[1:], " ") + "\n")})
		exit := int32(0)
		stream.queue(&pb.ExecOutput{ExitCode: &exit})
	} else if f.streams != nil {
		f.streams <- stream
	}
	stream.close()
	return stream, nil
}

func (f *fakeAttachAgent) ResizeExec(ctx context.Context, execID string, rows, cols uint32) error {
	f.mu.Lock()
	f.resizeLog = append(f.resizeLog, resizeCall{execID, rows, cols})
	f.mu.Unlock()
	if f.resize != nil {
		return f.resize(execID, rows, cols)
	}
	return nil
}

func (f *fakeAttachAgent) SignalExec(ctx context.Context, execID string, sig int32) error {
	f.mu.Lock()
	f.signalLog = append(f.signalLog, signalCall{execID, sig})
	f.mu.Unlock()
	if f.signal != nil {
		return f.signal(execID, sig)
	}
	return nil
}

type fakeExecStream struct {
	frames    chan *pb.ExecOutput
	stdin     bytes.Buffer
	closed    bool
	resizes   []resizeCall
	signals   []signalCall
	closeOnce bool
	mu        sync.Mutex
}

func (s *fakeExecStream) queue(out *pb.ExecOutput) {
	s.frames <- out
}

func (s *fakeExecStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.frames)
}

func (s *fakeExecStream) Recv() (*pb.ExecOutput, error) {
	out, ok := <-s.frames
	if !ok {
		return nil, io.EOF
	}
	return out, nil
}

func (s *fakeExecStream) SendStdin(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.stdin.Write(data)
	return err
}

func (s *fakeExecStream) SendResize(rows, cols uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resizes = append(s.resizes, resizeCall{rows: rows, cols: cols})
	return nil
}

func (s *fakeExecStream) SendSignal(signal int32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signals = append(s.signals, signalCall{sig: signal})
	return nil
}

func (s *fakeExecStream) CloseStdin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeOnce = true
	return nil
}

// TestServeAgentExecAttachEchoesStdoutAndExit drives the inner attach handler
// directly through a net.Pipe so we can read the JSON-line response framing
// without spinning up the real listener / VM stack.
func TestServeAgentExecAttachEchoesStdoutAndExit(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	cs := &ControlServer{}
	agent := &fakeAttachAgent{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, _ := json.Marshal(map[string]any{
			"type": "agent-exec-attach",
			"args": []string{"echo", "hi"},
		})
		cs.serveAgentExecAttach(server, raw, agent)
		server.Close()
	}()

	frames := readAttachFrames(t, client, 5*time.Second)
	<-done

	if len(frames) < 3 {
		t.Fatalf("expected >=3 frames (attached, stdout, done), got %d: %#v", len(frames), frames)
	}

	first := frames[0]
	if first.GetError() != "" {
		t.Fatalf("first frame error: %q", first.GetError())
	}
	var attached map[string]any
	mustJSON(t, first.GetData(), &attached)
	if attached["attached"] != true {
		t.Fatalf("first frame missing attached=true: %v", attached)
	}
	if id, _ := attached["exec_id"].(string); id == "" {
		t.Fatalf("first frame missing exec_id: %v", attached)
	}

	var sawStdout bool
	for _, f := range frames[1 : len(frames)-1] {
		var chunk map[string]any
		mustJSON(t, f.GetData(), &chunk)
		if chunk["stream"] == "stdout" {
			data, _ := chunk["data"].(string)
			decoded, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				t.Fatalf("decode stdout: %v", err)
			}
			if string(decoded) != "hi\n" {
				t.Fatalf("stdout = %q, want %q", decoded, "hi\n")
			}
			sawStdout = true
		}
	}
	if !sawStdout {
		t.Fatalf("no stdout frame observed: %#v", frames)
	}

	last := frames[len(frames)-1]
	var done2 map[string]any
	mustJSON(t, last.GetData(), &done2)
	if done2["done"] != true {
		t.Fatalf("last frame missing done=true: %v", done2)
	}
	if exit, _ := done2["exitCode"].(float64); exit != 0 {
		t.Fatalf("exitCode = %v, want 0", exit)
	}
}

func TestForwardAttachStdinSendsDecodedFrames(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	stream := &fakeExecStream{frames: make(chan *pb.ExecOutput)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardAttachStdin(context.Background(), server, "exec-1", stream)
	}()

	frame, _ := json.Marshal(agentExecStdinFrame{
		Type:   "stdin",
		ExecID: "exec-1",
		Data:   base64.StdEncoding.EncodeToString([]byte("abc")),
	})
	if _, err := client.Write(append(frame, '\n')); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	client.Close()
	<-done

	if got := stream.stdin.String(); got != "abc" {
		t.Fatalf("stdin = %q, want abc", got)
	}
}

func TestForwardAttachStdinSendsControlFrames(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	stream := &fakeExecStream{frames: make(chan *pb.ExecOutput)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardAttachStdin(context.Background(), server, "exec-1", stream)
	}()

	frames := []agentExecStdinFrame{
		{Type: "resize", ExecID: "exec-1", Rows: 40, Cols: 120},
		{Type: "signal", ExecID: "exec-1", Signal: 2},
		{Type: "close_stdin", ExecID: "exec-1"},
	}
	enc := json.NewEncoder(client)
	for _, frame := range frames {
		if err := enc.Encode(frame); err != nil {
			t.Fatalf("write frame: %v", err)
		}
	}
	<-done

	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.resizes) != 1 {
		t.Fatalf("resize calls = %d, want 1", len(stream.resizes))
	}
	if got := stream.resizes[0]; got.rows != 40 || got.cols != 120 {
		t.Fatalf("resize = %+v, want rows=40 cols=120", got)
	}
	if len(stream.signals) != 1 {
		t.Fatalf("signal calls = %d, want 1", len(stream.signals))
	}
	if got := stream.signals[0]; got.sig != 2 {
		t.Fatalf("signal = %+v, want sig=2", got)
	}
	if !stream.closeOnce {
		t.Fatalf("CloseStdin was not called")
	}
}

func TestServeAgentExecAttachConcurrentSessionsUseIndependentExecIDs(t *testing.T) {
	cs := &ControlServer{}
	agent := &fakeAttachAgent{attach: true, streams: make(chan *fakeExecStream, 2)}

	open := func() []map[string]any {
		server, client := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			raw, _ := json.Marshal(map[string]any{
				"type": "agent-exec-attach",
				"args": []string{"sh"},
			})
			cs.serveAgentExecAttach(server, raw, agent)
			server.Close()
		}()
		frames := readAttachFrames(t, client, 5*time.Second)
		client.Close()
		<-done
		var decoded []map[string]any
		for _, frame := range frames {
			var payload map[string]any
			mustJSON(t, frame.GetData(), &payload)
			decoded = append(decoded, payload)
		}
		return decoded
	}

	first := open()
	second := open()
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("missing attach frames: first=%v second=%v", first, second)
	}
	firstID, _ := first[0]["exec_id"].(string)
	secondID, _ := second[0]["exec_id"].(string)
	if firstID == "" || secondID == "" {
		t.Fatalf("missing exec ids: first=%v second=%v", first[0], second[0])
	}
	if firstID == secondID {
		t.Fatalf("exec ids matched: %q", firstID)
	}
	if first[0]["stdin"] != true || second[0]["stdin"] != true {
		t.Fatalf("stdin handshake = %v / %v, want true", first[0], second[0])
	}
}

// TestServeAgentExecAttachRejectsEmptyArgs guards the args==0 path; the
// handler should write an error frame and return without calling into the
// agent.
func TestServeAgentExecAttachRejectsEmptyArgs(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	cs := &ControlServer{}
	agent := &fakeAttachAgent{}

	go func() {
		raw, _ := json.Marshal(map[string]any{"type": "agent-exec-attach"})
		cs.serveAgentExecAttach(server, raw, agent)
		server.Close()
	}()

	frames := readAttachFrames(t, client, 2*time.Second)
	if len(frames) != 1 || frames[0].GetError() == "" {
		t.Fatalf("expected single error frame, got %#v", frames)
	}
	if !strings.Contains(frames[0].GetError(), "args required") {
		t.Fatalf("error = %q, want args required", frames[0].GetError())
	}
}

func TestHandleAgentExecResizeForwardsToAgent(t *testing.T) {
	agent := &fakeAttachAgent{}
	raw := []byte(`{"type":"agent-exec-resize","exec_id":"xyz","cols":120,"rows":40}`)
	resp := handleAgentExecResize(context.Background(), agent, raw)
	if resp.Error != "" {
		t.Fatalf("error: %q", resp.Error)
	}
	if !resp.Success {
		t.Fatalf("success = false")
	}
	if len(agent.resizeLog) != 1 {
		t.Fatalf("resize calls = %d, want 1", len(agent.resizeLog))
	}
	got := agent.resizeLog[0]
	if got.execID != "xyz" || got.cols != 120 || got.rows != 40 {
		t.Fatalf("resize call = %+v, want {xyz 40 120}", got)
	}
}

func TestHandleAgentExecResizeRejectsZeroDimensions(t *testing.T) {
	agent := &fakeAttachAgent{}
	resp := handleAgentExecResize(context.Background(), agent, []byte(`{"exec_id":"x","cols":0,"rows":24}`))
	if resp.Error == "" {
		t.Fatalf("expected error for cols=0")
	}
	if len(agent.resizeLog) != 0 {
		t.Fatalf("resize was called despite invalid input")
	}
}

func TestHandleAgentExecResizeRequiresExecID(t *testing.T) {
	agent := &fakeAttachAgent{}
	resp := handleAgentExecResize(context.Background(), agent, []byte(`{"cols":80,"rows":24}`))
	if resp.Error == "" || !strings.Contains(resp.Error, "exec_id required") {
		t.Fatalf("error = %q, want exec_id required", resp.Error)
	}
}

func TestHandleAgentExecSignalForwardsToAgent(t *testing.T) {
	agent := &fakeAttachAgent{}
	raw := []byte(`{"type":"agent-exec-signal","exec_id":"abc","signal":2}`)
	resp := handleAgentExecSignal(context.Background(), agent, raw)
	if resp.Error != "" {
		t.Fatalf("error: %q", resp.Error)
	}
	if len(agent.signalLog) != 1 {
		t.Fatalf("signal calls = %d, want 1", len(agent.signalLog))
	}
	got := agent.signalLog[0]
	if got.execID != "abc" || got.sig != 2 {
		t.Fatalf("signal call = %+v, want {abc 2}", got)
	}
}

func TestHandleAgentExecSignalRejectsZero(t *testing.T) {
	agent := &fakeAttachAgent{}
	resp := handleAgentExecSignal(context.Background(), agent, []byte(`{"exec_id":"x","signal":0}`))
	if resp.Error == "" {
		t.Fatalf("expected error for signal=0")
	}
}

// TestAgentExecResizeRejectsBadToken exercises the full control-socket path
// to confirm authorizeRequest gates the new commands.
func TestAgentExecResizeRejectsBadToken(t *testing.T) {
	// os.MkdirTemp keeps the path short — unix socket paths are capped
	// at 104 chars on Darwin and t.TempDir() routinely overshoots.
	dir, err := os.MkdirTemp("", "ctlat*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "c.sock")
	cs := NewControlServerWithVMDir(sockPath, dir)
	cs.authToken = "secret"

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cs.listener = ln
	cs.running.Store(true)
	defer cs.running.Store(false)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		cs.handleConnection(conn)
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	req := `{"type":"agent-exec-resize","auth_token":"wrong","exec_id":"x","cols":80,"rows":24}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	resp := &controlpb.ControlResponse{}
	if err := protojsonUnmarshaler.Unmarshal([]byte(line), resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.GetError() != "unauthorized" {
		t.Fatalf("expected unauthorized, got error=%q success=%v", resp.GetError(), resp.GetSuccess())
	}
}

// readAttachFrames decodes JSON-line ControlResponse frames from r until r
// is closed or the deadline elapses.
func readAttachFrames(t *testing.T, r net.Conn, timeout time.Duration) []*controlpb.ControlResponse {
	t.Helper()
	_ = r.SetReadDeadline(time.Now().Add(timeout))
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var out []*controlpb.ControlResponse
	for scanner.Scan() {
		line := scanner.Bytes()
		resp := &controlpb.ControlResponse{}
		if err := protojsonUnmarshaler.Unmarshal(line, resp); err != nil {
			t.Fatalf("decode frame: %v (line=%q)", err, line)
		}
		out = append(out, resp)
	}
	return out
}

func mustJSON(t *testing.T, data string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), dst); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
}
