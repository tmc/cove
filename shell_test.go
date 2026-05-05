package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// TestShellCommandRequiresVM confirms invocation with no positional args
// returns the "vm name required" error and prints usage to stderr.
func TestShellCommandRequiresVM(t *testing.T) {
	err := shellCommand(nil)
	if err == nil || !strings.Contains(err.Error(), "vm name required") {
		t.Fatalf("shellCommand(nil) error = %v, want vm name required", err)
	}
}

// TestResolveShellSocketUnknownVM verifies the friendly "no running VM"
// error fires before any dial attempt.
func TestResolveShellSocketUnknownVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := resolveShellSocket("does-not-exist-xyz")
	if err == nil || !strings.Contains(err.Error(), "no running VM at") {
		t.Fatalf("resolveShellSocket = %v, want no running VM error", err)
	}
}

// TestResolveShellSocketMissingSocket creates the VM dir but leaves the
// control socket missing — surfaces the second branch of the friendly error.
func TestResolveShellSocketMissingSocket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".vz", "vms", "myvm"), 0o755); err != nil {
		t.Fatalf("mkdir vm: %v", err)
	}
	_, err := resolveShellSocket("myvm")
	if err == nil || !strings.Contains(err.Error(), "control socket missing") {
		t.Fatalf("resolveShellSocket = %v, want control socket missing", err)
	}
}

// TestPumpShellFramesWritesStreamsAndExit drives the frame pump with a
// scripted server response: attach OK, one stdout chunk, one stderr chunk,
// then done(exit=7). Verifies stream routing and exit propagation.
func TestPumpShellFramesWritesStreamsAndExit(t *testing.T) {
	frames := []string{
		mustResponse(t, map[string]any{
			"stream": "stdout",
			"data":   base64.StdEncoding.EncodeToString([]byte("hello\n")),
		}),
		mustResponse(t, map[string]any{
			"stream": "stderr",
			"data":   base64.StdEncoding.EncodeToString([]byte("oops\n")),
		}),
		mustResponse(t, map[string]any{
			"done":     true,
			"exitCode": 7,
		}),
	}

	r := bufio.NewReader(strings.NewReader(strings.Join(frames, "\n") + "\n"))
	var out, errOut bytes.Buffer
	exit, err := pumpShellFrames(r, &out, &errOut)
	if err != nil {
		t.Fatalf("pumpShellFrames err = %v", err)
	}
	if exit != 7 {
		t.Fatalf("exit = %d, want 7", exit)
	}
	if got := out.String(); got != "hello\n" {
		t.Fatalf("stdout = %q, want %q", got, "hello\n")
	}
	if got := errOut.String(); got != "oops\n" {
		t.Fatalf("stderr = %q, want %q", got, "oops\n")
	}
}

// TestPumpShellFramesAgentDisconnectIsError confirms an EOF before `done`
// surfaces a clear error rather than a silent zero exit.
func TestPumpShellFramesAgentDisconnectIsError(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	var out, errOut bytes.Buffer
	_, err := pumpShellFrames(r, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "agent disconnected") {
		t.Fatalf("expected agent disconnected, got %v", err)
	}
}

// TestPumpShellFramesPropagatesServerError surfaces a non-empty
// ControlResponse.Error string as a guest agent error.
func TestPumpShellFramesPropagatesServerError(t *testing.T) {
	resp := &controlpb.ControlResponse{Error: "exec attach: no such command"}
	body, err := protojsonMarshaler.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := bufio.NewReader(strings.NewReader(string(body) + "\n"))
	var out, errOut bytes.Buffer
	_, err = pumpShellFrames(r, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "no such command") {
		t.Fatalf("expected guest agent error, got %v", err)
	}
}

func TestWriteShellStdinFrames(t *testing.T) {
	var out bytes.Buffer
	writer := &shellAttachWriter{w: &out}
	if err := writeShellStdinFrames(context.Background(), writer, "exec-1", strings.NewReader("abc")); err != nil {
		t.Fatalf("writeShellStdinFrames: %v", err)
	}
	var frame agentExecStdinFrame
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &frame); err != nil {
		t.Fatalf("decode stdin frame: %v", err)
	}
	if frame.Type != "stdin" || frame.ExecID != "exec-1" {
		t.Fatalf("frame = %+v, want stdin exec-1", frame)
	}
	decoded, err := base64.StdEncoding.DecodeString(frame.Data)
	if err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if string(decoded) != "abc" {
		t.Fatalf("stdin data = %q, want abc", decoded)
	}
}

func TestPumpShellFramesIgnoresAttachWarningPayload(t *testing.T) {
	frames := []string{
		mustResponse(t, map[string]any{
			"attached": true,
			"exec_id":  "exec-1",
			"stdin":    false,
			"warning":  "guest agent does not support ExecAttach; stdin disabled",
		}),
		mustResponse(t, map[string]any{
			"done":     true,
			"exitCode": 0,
		}),
	}
	r := bufio.NewReader(strings.NewReader(strings.Join(frames, "\n") + "\n"))
	var out, errOut bytes.Buffer
	exit, err := pumpShellFrames(r, &out, &errOut)
	if err != nil {
		t.Fatalf("pumpShellFrames err = %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
}

// TestMapAttachErrorVariants covers the friendly-error rewriter for the
// three named server-side error strings the dispatcher can return.
func TestMapAttachErrorVariants(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"unauthorized", "unauthorized", "control token mismatch"},
		{"agent not ready", "agent not ready", "guest agent not responding"},
		{"agent unavailable", "guest agent unavailable: ping failed", "guest agent not responding"},
		{"unknown", "exec attach: oops", "attach: exec attach: oops"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mapAttachError("vm0", c.raw)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("mapAttachError(%q) = %v, want substring %q", c.raw, err, c.want)
			}
		})
	}
}

// TestRunShellSessionEndToEnd spins up a fake control-socket server that
// emulates the Slice 1 attach handler, then runs runShellSession against
// it as a client. Stdin is /dev/null (matches Slice 2 limitation), so the
// server completes the canned exec and the client returns its exit code.
func TestRunShellSessionEndToEnd(t *testing.T) {
	dir, err := os.MkdirTemp("", "shellsess*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "c.sock")
	srv, err := newFakeShellServer(sockPath)
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer devnull.Close()

	out, outDone := newPipeBuffer()
	errBuf, errDone := newPipeBuffer()
	exit, err := runShellSession(
		context.Background(),
		sockPath,
		"", // no token configured
		"vm0",
		[]string{"echo", "fromfake"},
		devnull,
		out.w,
		errBuf.w,
	)
	if err != nil {
		t.Fatalf("runShellSession err = %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	// Close writer ends so the drain goroutines finish; then wait for them.
	out.w.Close()
	errBuf.w.Close()
	<-outDone
	<-errDone

	// Server should have observed exactly one attach.
	if got := srv.attachCount(); got != 1 {
		t.Fatalf("server attachCount = %d, want 1", got)
	}

	// And the synthesized stdout chunk should have arrived on out.
	if !strings.Contains(out.buf.String(), "fromfake") {
		t.Fatalf("stdout = %q, want substring fromfake", out.buf.String())
	}
}

func TestRunShellSessionV02FallbackWarnsAndCompletes(t *testing.T) {
	dir, err := os.MkdirTemp("", "shellsess*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "c.sock")
	srv, err := newFakeShellServer(sockPath)
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()
	srv.stdin = false
	srv.warning = "guest agent does not support ExecAttach; stdin disabled"

	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer devnull.Close()

	out, outDone := newPipeBuffer()
	errBuf, errDone := newPipeBuffer()
	exit, err := runShellSession(
		context.Background(),
		sockPath,
		"",
		"vm0",
		[]string{"echo", "fallback"},
		devnull,
		out.w,
		errBuf.w,
	)
	if err != nil {
		t.Fatalf("runShellSession err = %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	out.w.Close()
	errBuf.w.Close()
	<-outDone
	<-errDone

	if !strings.Contains(out.buf.String(), "fallback") {
		t.Fatalf("stdout = %q, want substring fallback", out.buf.String())
	}
	if !strings.Contains(errBuf.buf.String(), srv.warning) {
		t.Fatalf("stderr = %q, want warning %q", errBuf.buf.String(), srv.warning)
	}
}

// pipeBuffer pairs a bytes.Buffer sink with the writer end of an os.Pipe so
// production code that wants *os.File can write into a buffer the test can
// inspect. Caller must Close() w and then wait on the returned channel.
type pipeBuffer struct {
	w   *os.File
	buf *bytes.Buffer
}

func newPipeBuffer() (*pipeBuffer, <-chan struct{}) {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	pb := &pipeBuffer{w: w, buf: &bytes.Buffer{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer r.Close()
		_, _ = io.Copy(pb.buf, r)
	}()
	return pb, done
}

// fakeShellServer emulates the Slice 1 control-socket attach handler.
// Accepts one connection per attach, consumes the JSON-line request, then
// writes the attached/stdout/done frames the real server would produce.
type fakeShellServer struct {
	ln       net.Listener
	mu       sync.Mutex
	attaches int
	closed   chan struct{}
	stdin    bool
	warning  string
}

func newFakeShellServer(sockPath string) (*fakeShellServer, error) {
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	s := &fakeShellServer{ln: ln, closed: make(chan struct{}), stdin: true}
	go s.serve()
	return s, nil
}

func (s *fakeShellServer) Close() error {
	close(s.closed)
	return s.ln.Close()
}

func (s *fakeShellServer) attachCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attaches
}

func (s *fakeShellServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
				return
			}
		}
		go s.handle(conn)
	}
}

func (s *fakeShellServer) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return
	}
	if req["type"] != "agent-exec-attach" {
		// Sidecar dispatches (resize/signal): respond ok and exit.
		_, _ = conn.Write([]byte(mustResponse(nil, map[string]any{}) + "\n"))
		return
	}
	s.mu.Lock()
	s.attaches++
	s.mu.Unlock()

	args, _ := req["args"].([]any)
	execID := "fake-exec-1"

	// 1. attached handshake
	_, _ = conn.Write([]byte(mustResponse(nil, map[string]any{
		"attached": true,
		"exec_id":  execID,
		"stdin":    s.stdin,
		"warning":  s.warning,
	}) + "\n"))

	// 2. one stdout frame echoing args[1:]
	var payload string
	if len(args) > 1 {
		var parts []string
		for _, a := range args[1:] {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		payload = strings.Join(parts, " ") + "\n"
	}
	_, _ = conn.Write([]byte(mustResponse(nil, map[string]any{
		"stream":  "stdout",
		"data":    base64.StdEncoding.EncodeToString([]byte(payload)),
		"exec_id": execID,
	}) + "\n"))

	// 3. done with exit 0
	_, _ = conn.Write([]byte(mustResponse(nil, map[string]any{
		"done":     true,
		"exec_id":  execID,
		"exitCode": 0,
	}) + "\n"))

	// Give the client a moment to drain before closing.
	time.Sleep(20 * time.Millisecond)
}

// mustResponse marshals a ControlResponse{Success:true, Data: <jsonified payload>}
// the same way the production writeResponse does. The *testing.T is
// optional (nil-safe) so the helper is callable from goroutines without
// passing the test through.
func mustResponse(t *testing.T, payload map[string]any) string {
	body, err := json.Marshal(payload)
	if err != nil {
		if t != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		return ""
	}
	resp := &controlpb.ControlResponse{Success: true, Data: string(body)}
	out, err := protojsonMarshaler.Marshal(resp)
	if err != nil {
		if t != nil {
			t.Fatalf("marshal resp: %v", err)
		}
		return ""
	}
	return string(out)
}
