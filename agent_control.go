// agent_control.go - Bridge between control socket and GRPC guest agent.
//
// Extends the control socket with agent commands that delegate to the
// vz-agent daemon running inside the guest. The host connects to the agent
// over vsock port 1024 via VZVirtioSocketDevice.
//
// New command types:
//
//	agent-connect  - Establish vsock connection to guest agent
//	agent-ping     - Check if agent is alive
//	agent-info     - Get guest system information
//	agent-exec     - Run a command in the guest
//	agent-read     - Read a file from the guest
//	agent-write    - Write a file to the guest
//	agent-shutdown - Graceful guest shutdown via agent
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// ensureAgent connects to the guest agent if not already connected.
// Caller must hold s.agentMu.
func (s *ControlServer) ensureAgent() error {
	if s.agent != nil {
		// Quick health check: if the connection is dead, reconnect.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := s.agent.Ping(ctx); err == nil {
			return nil
		}
		// Connection is dead, reconnect.
		s.agent.Close()
		s.agent = nil
	}
	return s.connectAgentLocked()
}

// connectAgentLocked establishes the agent connection.
// Caller must hold s.agentMu.
func (s *ControlServer) connectAgentLocked() error {
	if s.agent != nil {
		return nil // already connected
	}

	mgr, err := NewVsockDeviceManager(s.vm, s.vmQueue)
	if err != nil {
		return fmt.Errorf("vsock device: %w (is the VM running?)", err)
	}

	conn, err := mgr.ConnectToAgent(agentPort)
	if err != nil {
		return fmt.Errorf("connect agent: %w (is the guest agent running? check /var/log/vz-agent.log inside the VM)", err)
	}

	client, err := NewAgentClient(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("agent client: %w", err)
	}

	s.agent = client
	return nil
}

// handleAgentCommand dispatches agent-* commands from the control socket.
// Returns ok=true if the command was handled, ok=false if not an agent command.
// Uses agentMu (not mu) so long-running agent-exec doesn't block other operations.
func (s *ControlServer) handleAgentCommand(req *controlpb.ControlRequest) (resp *controlpb.ControlResponse, ok bool) {
	// Quick check: is this an agent command?
	if !strings.HasPrefix(req.Type, "agent-") {
		return nil, false
	}
	s.agentMu.Lock()
	defer s.agentMu.Unlock()

	switch req.Type {
	case "agent-connect":
		return s.handleAgentConnect(), true
	case "agent-ping":
		return s.handleAgentPing(), true
	case "agent-info":
		return s.handleAgentInfo(), true
	case "agent-exec":
		cmd := req.GetAgentExec()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing agent-exec command payload"}, true
		}
		return s.handleAgentExec(cmd), true
	case "agent-exec-stream":
		return &controlpb.ControlResponse{
			Error: "agent-exec-stream requires streaming transport (use one request per connection)",
		}, true
	case "agent-read":
		cmd := req.GetAgentRead()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing agent-read command payload"}, true
		}
		return s.handleAgentRead(cmd), true
	case "agent-write":
		cmd := req.GetAgentWrite()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing agent-write command payload"}, true
		}
		return s.handleAgentWrite(cmd), true
	case "agent-shutdown":
		cmd := req.GetAgentShutdown()
		if cmd == nil {
			cmd = &controlpb.AgentShutdownCommand{}
		}
		return s.handleAgentShutdown(cmd), true
	case "agent-reboot":
		return s.handleAgentReboot(), true
	case "agent-sshd":
		cmd := req.GetAgentSshd()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing agent-sshd command payload"}, true
		}
		return s.handleAgentSSHD(cmd), true
	case "agent-cp":
		cmd := req.GetAgentCp()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing agent-cp command payload"}, true
		}
		return s.handleAgentCopy(cmd), true
	case "agent-mount-volumes":
		return s.handleAgentMountVolumes(), true
	default:
		return nil, false
	}
}

func (s *ControlServer) handleAgentConnect() *controlpb.ControlResponse {
	// Force reconnect: close any stale connection first.
	if s.agent != nil {
		s.agent.Close()
		s.agent = nil
	}
	if err := s.connectAgentLocked(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return &controlpb.ControlResponse{Success: true, Data: "connected to guest agent", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "connected to guest agent"}}}
}

func (s *ControlServer) handleAgentPing() *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := s.agent.Ping(ctx)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("ping: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    fmt.Sprintf("agent version: %s", version),
		Result:  &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: version}},
	}
}

func (s *ControlServer) handleAgentInfo() *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := s.agent.Info(ctx)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("info: %v", err)}
	}
	data, _ := json.Marshal(info)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentInfo{AgentInfo: &controlpb.AgentInfoResponse{
			Hostname: info.GetHostname(),
			Os:       info.GetOsVersion(),
			Arch:     info.GetArch(),
			RawJson:  string(data),
		}},
	}
}

func (s *ControlServer) handleAgentExec(cmd *controlpb.AgentExecCommand) *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if len(cmd.Args) == 0 {
		return &controlpb.ControlResponse{Error: "args required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := s.agent.Exec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("exec: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"exitCode": result.ExitCode,
		"stdout":   string(result.Stdout),
		"stderr":   string(result.Stderr),
		"duration": result.DurationSeconds,
	})
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
			ExitCode:        result.ExitCode,
			Stdout:          string(result.Stdout),
			Stderr:          string(result.Stderr),
			DurationSeconds: result.DurationSeconds,
		}},
	}
}

func (s *ControlServer) handleAgentRead(cmd *controlpb.AgentFileReadCommand) *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if cmd.Path == "" {
		return &controlpb.ControlResponse{Error: "path required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := s.agent.ReadFile(ctx, cmd.Path)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("read: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(data),
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Data: data}},
	}
}

func (s *ControlServer) handleAgentWrite(cmd *controlpb.AgentFileWriteCommand) *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if cmd.Path == "" {
		return &controlpb.ControlResponse{Error: "path required"}
	}
	data, err := base64.StdEncoding.DecodeString(cmd.Data)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("decode data: %v", err)}
	}
	mode := cmd.Mode
	if mode == 0 {
		mode = 0644
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.agent.WriteFile(ctx, cmd.Path, data, mode); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("write: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "ok", Result: &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: "ok"}}}
}

func (s *ControlServer) handleAgentShutdown(cmd *controlpb.AgentShutdownCommand) *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.agent.Shutdown(ctx, cmd.Force); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("shutdown: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "shutdown initiated", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "shutdown initiated"}}}
}

func (s *ControlServer) handleAgentReboot() *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.agent.Reboot(ctx); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("reboot: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "reboot initiated", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "reboot initiated"}}}
}

func (s *ControlServer) handleAgentSSHD(cmd *controlpb.AgentSSHDCommand) *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	var args []string
	switch cmd.Action {
	case "on":
		args = []string{"launchctl", "load", "-w", "/System/Library/LaunchDaemons/ssh.plist"}
	case "off":
		args = []string{"launchctl", "unload", "-w", "/System/Library/LaunchDaemons/ssh.plist"}
	case "status":
		args = []string{"systemsetup", "-getremotelogin"}
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown sshd action: %s (use on, off, or status)", cmd.Action)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := s.agent.Exec(ctx, args, nil, "")
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("sshd %s: %v", cmd.Action, err)}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"exitCode": result.ExitCode,
		"stdout":   string(result.Stdout),
		"stderr":   string(result.Stderr),
	})
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
			ExitCode: result.ExitCode,
			Stdout:   string(result.Stdout),
			Stderr:   string(result.Stderr),
		}},
	}
}

func (s *ControlServer) handleAgentMountVolumes() *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	cfg, err := LoadVMConfig(s.effectiveVMDir())
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("load config: %v", err)}
	}

	tagged := taggedVolumes(cfg.Volumes)
	if len(tagged) == 0 {
		return &controlpb.ControlResponse{Success: true, Data: "no tagged volumes to mount", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "no tagged volumes to mount"}}}
	}

	var results []map[string]interface{}
	for _, m := range tagged {
		mountPoint := "/Volumes/" + m.Tag
		if linuxMode {
			mountPoint = "/mnt/" + m.Tag
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s.agent.Exec(ctx, []string{"mkdir", "-p", mountPoint}, nil, "")
		cancel()

		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		result, err := s.agent.Exec(ctx, []string{"mount_virtiofs", m.Tag, mountPoint}, nil, "")
		cancel()

		entry := map[string]interface{}{
			"tag":        m.Tag,
			"mountPoint": mountPoint,
		}
		if err != nil {
			entry["error"] = err.Error()
		} else if result.ExitCode != 0 {
			entry["error"] = string(result.Stderr)
		} else {
			entry["mounted"] = true
		}
		results = append(results, entry)
	}

	data, _ := json.Marshal(results)
	return &controlpb.ControlResponse{Success: true, Data: string(data), Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: string(data)}}}
}

func (s *ControlServer) handleAgentExecStreamConnection(conn net.Conn, req *controlpb.ControlRequest) {
	cmd := req.GetAgentExec()
	if cmd == nil {
		writeResponse(conn, &controlpb.ControlResponse{Error: "missing agent-exec command payload"})
		return
	}
	if len(cmd.Args) == 0 {
		writeResponse(conn, &controlpb.ControlResponse{Error: "args required"})
		return
	}

	s.agentMu.Lock()
	defer s.agentMu.Unlock()

	if err := s.ensureAgent(); err != nil {
		writeResponse(conn, &controlpb.ControlResponse{Error: err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	stream, err := s.agent.ExecStream(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	if err != nil {
		writeResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("exec stream: %v", err)})
		return
	}

	var finalExitCode int32
	for {
		out, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeResponse(conn, &controlpb.ControlResponse{Error: fmt.Sprintf("recv stream: %v", err)})
			return
		}

		if len(out.Data) > 0 {
			streamName := "stdout"
			if out.Stream == 1 {
				streamName = "stderr"
			}
			chunkPayload, _ := json.Marshal(map[string]any{
				"stream": streamName,
				"data":   base64.StdEncoding.EncodeToString(out.Data),
			})
			writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(chunkPayload)})
		}

		if out.ExitCode != nil {
			finalExitCode = *out.ExitCode
		}
	}

	donePayload, _ := json.Marshal(map[string]any{
		"done":     true,
		"exitCode": finalExitCode,
	})
	writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(donePayload)})
}

func (s *ControlServer) handleAgentCopy(cmd *controlpb.AgentCopyCommand) *controlpb.ControlResponse {
	if err := s.ensureAgent(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if cmd.HostPath == "" || cmd.GuestPath == "" {
		return &controlpb.ControlResponse{Error: "host_path and guest_path required"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if cmd.ToGuest {
		info, err := os.Stat(cmd.HostPath)
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("stat %s: %v", cmd.HostPath, err)}
		}
		mode := os.FileMode(cmd.Mode)
		if mode == 0 {
			mode = info.Mode()
		}
		if err := s.agent.CopyToGuest(ctx, cmd.HostPath, cmd.GuestPath, mode); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
		}
		msg := fmt.Sprintf("%s -> guest:%s (%d bytes)", cmd.HostPath, cmd.GuestPath, info.Size())
		return &controlpb.ControlResponse{
			Success: true,
			Data:    msg,
			Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: msg}},
		}
	}

	// Guest to host.
	if err := s.agent.CopyFromGuest(ctx, cmd.GuestPath, cmd.HostPath); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
	}
	info, _ := os.Stat(cmd.HostPath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	msg := fmt.Sprintf("guest:%s -> %s (%d bytes)", cmd.GuestPath, cmd.HostPath, size)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    msg,
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: msg}},
	}
}
