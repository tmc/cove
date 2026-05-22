// Package control owns the VM control socket edge.
package control

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

var (
	ProtoJSONMarshaler = protojson.MarshalOptions{
		UseProtoNames: true,
	}
	ProtoJSONUnmarshaler = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

type Handler interface {
	Authorize(token string) bool
	HandleStream(conn net.Conn, req *controlpb.ControlRequest, raw []byte) (handled bool, closeConn bool)
	HandleRaw(req *controlpb.ControlRequest, raw []byte) (*controlpb.ControlResponse, bool)
	Handle(req *controlpb.ControlRequest) *controlpb.ControlResponse
	Event(reqType string, resp *controlpb.ControlResponse)
}

type Server struct {
	SocketPath    string
	Verbose       bool
	AuthTokenPath string
	Handler       Handler
	HealthMonitor func()
	ActiveLimit   int32
	StopTimeout   time.Duration
	AcceptError   func(error)
	Started       func()
	listener      net.Listener
	running       atomic.Bool
	active        atomic.Int32
	rejected      atomic.Uint64
	wg            sync.WaitGroup
	connMu        sync.Mutex
	conns         map[net.Conn]struct{}
}

func (s *Server) Start(ctx context.Context) error {
	if s.ActiveLimit <= 0 {
		s.ActiveLimit = 64
	}
	if s.Handler == nil {
		return fmt.Errorf("control server handler is nil")
	}
	if s.SocketPath == "" {
		return fmt.Errorf("control socket path is required")
	}
	_ = os.Remove(s.SocketPath)
	listener, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on socket: %w", err)
	}
	if err := os.Chmod(s.SocketPath, 0600); err != nil {
		listener.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.listener = listener
	s.running.Store(true)
	if s.Started != nil {
		s.Started()
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.acceptLoop()
	}()
	if s.HealthMonitor != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.HealthMonitor()
		}()
	}
	go func() {
		<-ctx.Done()
		s.Stop()
	}()
	return nil
}

func (s *Server) Stop() {
	s.running.Store(false)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.closeActiveConnections()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	timeout := s.StopTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
	if s.SocketPath != "" {
		_ = os.Remove(s.SocketPath)
	}
}

// Rejected returns the count of accepted connections that were dropped
// because the active-connection limit was exceeded. The returned value
// is monotonically increasing over the server's lifetime.
func (s *Server) Rejected() uint64 {
	if s == nil {
		return 0
	}
	return s.rejected.Load()
}

func (s *Server) acceptLoop() {
	for s.running.Load() {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.running.Load() {
				if s.AcceptError != nil {
					s.AcceptError(err)
				}
			}
			continue
		}
		if s.active.Add(1) > s.ActiveLimit {
			s.active.Add(-1)
			s.rejected.Add(1)
			_ = conn.Close()
			continue
		}
		s.trackConn(conn)
		go func() {
			defer func() {
				s.untrackConn(conn)
				s.active.Add(-1)
			}()
			ServeConnection(conn, s.Handler)
		}()
	}
}

func (s *Server) trackConn(conn net.Conn) {
	s.connMu.Lock()
	if s.conns == nil {
		s.conns = make(map[net.Conn]struct{})
	}
	s.conns[conn] = struct{}{}
	s.connMu.Unlock()
}

func (s *Server) untrackConn(conn net.Conn) {
	s.connMu.Lock()
	delete(s.conns, conn)
	s.connMu.Unlock()
}

func (s *Server) closeActiveConnections() {
	s.connMu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.connMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func ServeConnection(conn net.Conn, h Handler) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if err := conn.SetDeadline(time.Now().Add(5 * time.Minute)); err != nil {
		return
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req controlpb.ControlRequest
		if err := ProtoJSONUnmarshaler.Unmarshal([]byte(line), &req); err != nil {
			WriteResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}
		PopulateLegacyRequestPayloads(line, &req)
		if !h.Authorize(req.AuthToken) {
			WriteResponse(conn, &controlpb.ControlResponse{Error: "unauthorized"})
			continue
		}

		if handled, closeConn := h.HandleStream(conn, &req, []byte(line)); handled {
			if closeConn {
				return
			}
			if err := conn.SetDeadline(time.Now().Add(5 * time.Minute)); err != nil {
				return
			}
			continue
		}
		if resp, ok := h.HandleRaw(&req, []byte(line)); ok {
			WriteResponse(conn, resp)
			continue
		}

		resp := h.Handle(&req)
		WriteResponse(conn, resp)
		h.Event(req.Type, resp)
		if err := conn.SetDeadline(time.Now().Add(5 * time.Minute)); err != nil {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		WriteResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("read request: %v", err)})
	}
}

func WriteResponse(conn net.Conn, resp *controlpb.ControlResponse) error {
	data, err := ProtoJSONMarshaler.Marshal(resp)
	if err != nil {
		slog.Error("control socket: marshal response", slog.Any("err", err))
		return err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		slog.Error("control socket: write response", slog.Any("err", err))
		return err
	}
	return nil
}

func PopulateLegacyRequestPayloads(line string, req *controlpb.ControlRequest) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return
	}
	populateLegacyAuthToken(raw, req)

	switch req.Type {
	case "screenshot":
		populateLegacyScreenshot(raw, req)
	case "snapshot":
		populateLegacySnapshot(raw, req)
	case "memory":
		populateLegacyMemory(raw, req)
	case "agent-exec", "agent-exec-stream":
		populateLegacyAgentExec(raw, req)
	}
}

func populateLegacyAuthToken(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.AuthToken != "" {
		return
	}
	if blob, ok := raw["token"]; ok {
		var v string
		if err := json.Unmarshal(blob, &v); err == nil {
			req.AuthToken = v
		}
	}
}

func populateLegacyScreenshot(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetScreenshot() != nil {
		return
	}
	cmd := &controlpb.ScreenshotCommand{}
	seen := false

	if blob, ok := raw["screenshot"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}
	if blob, ok := raw["diff"]; ok {
		var v bool
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Diff = v
			seen = true
		}
	}
	if blob, ok := raw["scale"]; ok {
		var v float64
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Scale = v
			seen = true
		}
	}
	if blob, ok := raw["quality"]; ok {
		var v int32
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Quality = v
			seen = true
		}
	}
	if blob, ok := raw["format"]; ok {
		var v string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Format = v
			seen = true
		}
	}
	if seen {
		req.Command = &controlpb.ControlRequest_Screenshot{Screenshot: cmd}
	}
}

func populateLegacySnapshot(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetSnapshot() != nil {
		return
	}
	cmd := &controlpb.SnapshotCommand{}
	seen := false

	if blob, ok := raw["snapshot"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}
	type snapshotPayload struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if blob, ok := raw["data"]; ok {
		var payload snapshotPayload
		if err := json.Unmarshal(blob, &payload); err == nil {
			if payload.Action != "" {
				cmd.Action = payload.Action
			}
			if payload.Name != "" {
				cmd.Name = payload.Name
			}
			seen = seen || payload.Action != "" || payload.Name != ""
		}
	}
	if seen {
		req.Command = &controlpb.ControlRequest_Snapshot{Snapshot: cmd}
	}
}

func populateLegacyMemory(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetMemory() != nil {
		return
	}
	cmd := &controlpb.MemoryCommand{}
	seen := false

	if blob, ok := raw["memory"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}
	type memoryPayload struct {
		Action string  `json:"action"`
		SizeGB float64 `json:"size_gb"`
	}
	if blob, ok := raw["data"]; ok {
		var payload memoryPayload
		if err := json.Unmarshal(blob, &payload); err == nil {
			if payload.Action != "" {
				cmd.Action = payload.Action
			}
			if payload.SizeGB != 0 {
				cmd.SizeGb = payload.SizeGB
			}
			seen = seen || payload.Action != "" || payload.SizeGB != 0
		}
	}
	if seen {
		req.Command = &controlpb.ControlRequest_Memory{Memory: cmd}
	}
}

func populateLegacyAgentExec(raw map[string]json.RawMessage, req *controlpb.ControlRequest) {
	if req.GetAgentExec() != nil {
		return
	}
	cmd := &controlpb.AgentExecCommand{}
	seen := false

	if blob, ok := raw["agent_exec"]; ok {
		if err := json.Unmarshal(blob, cmd); err == nil {
			seen = true
		}
	}
	if blob, ok := raw["args"]; ok {
		var v []string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Args = v
			seen = true
		}
	}
	if blob, ok := raw["env"]; ok {
		var v map[string]string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.Env = v
			seen = true
		}
	}
	if blob, ok := raw["working_dir"]; ok {
		var v string
		if err := json.Unmarshal(blob, &v); err == nil {
			cmd.WorkingDir = v
			seen = true
		}
	}
	if seen {
		req.Command = &controlpb.ControlRequest_AgentExec{AgentExec: cmd}
	}
}
