// agent_control_attach.go - Slice 1 of design 023 (cove shell exec UX).
//
// Adds three control-socket commands that brokers a future `cove shell <vm>`
// client through the per-VM control socket and into the in-process agent
// client. The VM-owning cove process is the only one that can dial vsock to
// the guest; cross-process clients reach the agent via the control socket.
//
// Commands added:
//
//	agent-exec-attach  - Open ExecStreamControl with tty=true; pump
//	                     ExecOutput frames back to the client. Stdin frames
//	                     are forwarded through ExecAttach when the guest
//	                     agent advertises the v0.3 exec_attach feature.
//	                     Older agents fall back to ExecStreamControl and
//	                     read-only stdin.
//	agent-exec-resize  - Forward {exec_id, cols, rows} to ResizeExec.
//	agent-exec-signal  - Forward {exec_id, signal} to SignalExec.
//
// Auth uses the same control.token mechanism as every other agent-* command:
// the dispatcher validates s.authorizeRequest before reaching this file.
//
// The two non-streaming commands are testable without a live VM via the
// inner *JSONFor variants, which take the agent client as an interface so a
// fake can stand in for an *agentstate.AgentClient.

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

// attachAgent is the subset of *agentstate.AgentClient that the attach
// handlers need. Defined as an interface so tests can substitute a fake
// without standing up a connect-go server.
type attachAgent interface {
	ExecAttachSupported(ctx context.Context) (bool, error)
	ExecAttachControl(ctx context.Context, execID string, tty bool, user string, args []string, env map[string]string, workDir string) (agentstate.ExecAttachStream, error)
	ExecStreamControl(ctx context.Context, execID string, tty bool, user string, args []string, env map[string]string, workDir string) (agentstate.ExecStreamReceiver, error)
	ResizeExec(ctx context.Context, execID string, rows, cols uint32) error
	SignalExec(ctx context.Context, execID string, signal int32) error
}

// agentExecAttachRequest is the payload for `agent-exec-attach`. Parsed from
// the raw JSON line because Slice 1 deliberately ships no proto bump (see
// design 023). Fields mirror controlpb.AgentExecCommand plus the exec_id the
// client may pre-allocate so resize/signal can address it.
type agentExecAttachRequest struct {
	ExecID     string            `json:"exec_id"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
	WorkingDir string            `json:"working_dir"`
	User       string            `json:"user"`
	TTY        *bool             `json:"tty"`
}

// agentExecResizeRequest is the payload for `agent-exec-resize`.
type agentExecResizeRequest struct {
	ExecID string `json:"exec_id"`
	Cols   uint32 `json:"cols"`
	Rows   uint32 `json:"rows"`
}

// agentExecSignalRequest is the payload for `agent-exec-signal`.
type agentExecSignalRequest struct {
	ExecID string `json:"exec_id"`
	Signal int32  `json:"signal"`
}

// agentExecStdinFrame is the inbound frame the client may send on the
// attach connection. In v0.3 it is forwarded to the guest through ExecAttach;
// older agents fall back to read-only stdin.
type agentExecStdinFrame struct {
	Type   string `json:"type"`
	ExecID string `json:"exec_id"`
	Data   string `json:"data"`
	Cols   uint32 `json:"cols"`
	Rows   uint32 `json:"rows"`
	Signal int32  `json:"signal"`
}

// handleAgentExecAttachConnection serves an `agent-exec-attach` request on a
// long-lived connection: it opens a tty=true exec stream against the agent
// and pumps ExecOutput frames back to the client until the stream ends or
// the client disconnects. Stdin frames from the client are read concurrently
// and forwarded when the agent supports ExecAttach.
func (s *ControlServer) handleAgentExecAttachConnection(conn net.Conn, raw []byte) {
	a, err := s.getAgent()
	if err != nil {
		writeResponse(conn, &controlpb.ControlResponse{Error: err.Error()})
		return
	}
	s.serveAgentExecAttach(conn, raw, a)
}

// serveAgentExecAttach is the testable inner loop: takes the agent as an
// interface so a fake can stand in. Returns after the exec stream closes.
func (s *ControlServer) serveAgentExecAttach(conn net.Conn, raw []byte, a attachAgent) {
	var req agentExecAttachRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("invalid attach payload: %v", err)})
		return
	}
	if len(req.Args) == 0 {
		writeResponse(conn, &controlpb.ControlResponse{Error: "args required"})
		return
	}
	if req.ExecID == "" {
		req.ExecID = fmt.Sprintf("cove-shell-%d", time.Now().UnixNano())
	}

	ctx, cancel := context.WithCancel(s.lifecycleContext())
	defer cancel()

	var (
		stream      agentstate.ExecStreamReceiver
		stdin       agentstate.ExecAttachStream
		stdinOK     bool
		warningText string
		err         error
	)
	stdinOK, err = a.ExecAttachSupported(ctx)
	if err != nil {
		stdinOK = false
		warningText = fmt.Sprintf("guest agent feature probe failed; stdin disabled: %v", err)
	}
	tty := true
	if req.TTY != nil {
		tty = *req.TTY
	}
	if stdinOK {
		stdin, err = a.ExecAttachControl(ctx, req.ExecID, tty, req.User, req.Args, req.Env, req.WorkingDir)
		stream = stdin
	} else {
		stream, err = a.ExecStreamControl(ctx, req.ExecID, tty, req.User, req.Args, req.Env, req.WorkingDir)
		if warningText == "" {
			warningText = "guest agent is older; interactive stdin disabled, using exec fallback"
		}
	}
	if err != nil {
		writeResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("exec attach: %v", err)})
		return
	}

	// Tell the client the exec is live and which exec_id to use for the
	// resize/signal sidecar commands.
	startPayload, _ := json.Marshal(map[string]any{
		"attached": true,
		"exec_id":  req.ExecID,
		"stdin":    stdinOK,
		"warning":  warningText,
	})
	if err := writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(startPayload)}); err != nil {
		return
	}

	if stdinOK {
		go forwardAttachStdin(ctx, conn, req.ExecID, stdin)
	} else {
		go drainAttachStdin(ctx, conn)
	}

	var finalExitCode int32
	for {
		out, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			writeResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("recv stream: %v", recvErr)})
			return
		}

		if len(out.Data) > 0 {
			streamName := "stdout"
			if out.Stream == 1 {
				streamName = "stderr"
			}
			chunkPayload, _ := json.Marshal(map[string]any{
				"stream":  streamName,
				"data":    base64.StdEncoding.EncodeToString(out.Data),
				"exec_id": req.ExecID,
			})
			if err := writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(chunkPayload)}); err != nil {
				return
			}
		}

		if out.ExitCode != nil {
			finalExitCode = *out.ExitCode
		}
	}

	donePayload, _ := json.Marshal(map[string]any{
		"done":     true,
		"exec_id":  req.ExecID,
		"exitCode": finalExitCode,
	})
	writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(donePayload)})
}

// drainAttachStdin reads JSON-line frames from conn and discards them. It is
// used only when the guest agent is old enough that stdin must degrade to
// read-only behavior.
func drainAttachStdin(ctx context.Context, conn net.Conn) {
	dec := json.NewDecoder(conn)
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		var frame agentExecStdinFrame
		if err := dec.Decode(&frame); err != nil {
			return
		}
		// Slice 1: stdin discarded. Decoded only to confirm it parses so a
		// malformed frame surfaces quickly during client development.
		_ = frame
	}
}

func forwardAttachStdin(ctx context.Context, conn net.Conn, execID string, stream agentstate.ExecAttachStream) {
	defer stream.CloseStdin()
	dec := json.NewDecoder(conn)
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		var frame agentExecStdinFrame
		if err := dec.Decode(&frame); err != nil {
			return
		}
		if frame.ExecID != execID {
			continue
		}
		switch frame.Type {
		case "stdin":
			if frame.Data == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(frame.Data)
			if err != nil {
				return
			}
			if err := stream.SendStdin(data); err != nil {
				return
			}
		case "resize":
			if frame.Rows == 0 || frame.Cols == 0 {
				continue
			}
			if err := stream.SendResize(frame.Rows, frame.Cols); err != nil {
				return
			}
		case "signal":
			if frame.Signal == 0 {
				continue
			}
			if err := stream.SendSignal(frame.Signal); err != nil {
				return
			}
		case "close_stdin":
			return
		}
	}
}

// handleAgentExecResizeJSON parses an agent-exec-resize payload and forwards
// it to ResizeExec. Inner function takes the agent as an interface for tests.
func (s *ControlServer) handleAgentExecResizeJSON(raw []byte) *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return handleAgentExecResize(s.lifecycleContext(), a, raw)
}

func handleAgentExecResize(ctx context.Context, a attachAgent, raw []byte) *controlpb.ControlResponse {
	var req agentExecResizeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("invalid resize payload: %v", err)}
	}
	if req.ExecID == "" {
		return &controlpb.ControlResponse{Error: "exec_id required"}
	}
	if req.Cols == 0 || req.Rows == 0 {
		return &controlpb.ControlResponse{Error: "cols and rows must be > 0"}
	}
	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.ResizeExec(rpcCtx, req.ExecID, req.Rows, req.Cols); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("resize: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "ok"}
}

// handleAgentExecSignalJSON parses an agent-exec-signal payload and forwards
// it to SignalExec.
func (s *ControlServer) handleAgentExecSignalJSON(raw []byte) *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return handleAgentExecSignal(s.lifecycleContext(), a, raw)
}

func handleAgentExecSignal(ctx context.Context, a attachAgent, raw []byte) *controlpb.ControlResponse {
	var req agentExecSignalRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("invalid signal payload: %v", err)}
	}
	if req.ExecID == "" {
		return &controlpb.ControlResponse{Error: "exec_id required"}
	}
	if req.Signal == 0 {
		return &controlpb.ControlResponse{Error: "signal must be non-zero"}
	}
	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.SignalExec(rpcCtx, req.ExecID, req.Signal); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("signal: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "ok"}
}
