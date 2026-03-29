// agent_control.go - Bridge between control socket and GRPC guest agents.
//
// Extends the control socket with agent commands that delegate to the
// vz-agent processes running inside the guest. Two agents:
//
//   - Daemon (port 1024): root context, system ops
//   - User agent (port 1025): user session, TCC/FDA grants
//
// New command types:
//
//	agent-connect      - Establish vsock connection to guest daemon agent
//	agent-ping         - Check if daemon agent is alive
//	agent-info         - Get guest system information
//	agent-exec         - Run a command in the guest (as root)
//	agent-user-exec    - Run a command in the user session (TCC/FDA)
//	agent-read         - Read a file from the guest
//	agent-write        - Write a file to the guest
//	agent-shutdown     - Graceful guest shutdown via agent
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// getAgent returns the current agent client, connecting if necessary.
// It holds agentMu only briefly for connection setup, not during RPCs.
func (s *ControlServer) getAgent() (*AgentClient, error) {
	state, err := s.currentVMState()
	if err != nil {
		return nil, err
	}
	if err := agentUnavailableForVMState(state); err != nil {
		return nil, err
	}

	// Fast path: read lock to check existing connection.
	s.agentMu.RLock()
	if a := s.agent; a != nil {
		s.agentMu.RUnlock()
		// Quick health check outside any lock.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := a.Ping(ctx); err == nil {
			return a, nil
		}
		// Connection is dead, fall through to reconnect.
	} else {
		s.agentMu.RUnlock()
	}

	// Slow path: write lock to reconnect.
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	// Double-check after acquiring write lock.
	if s.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := s.agent.Ping(ctx); err == nil {
			return s.agent, nil
		}
		s.agent.Close()
		s.agent = nil
	}
	if err := s.connectAgentLocked(); err != nil {
		return nil, err
	}
	return s.agent, nil
}

// getUserAgent returns the user session agent client, connecting if necessary.
// If the LaunchAgent is missing in a macOS guest, it is bootstrapped on demand.
func (s *ControlServer) getUserAgent() (*UserAgentClient, error) {
	state, err := s.currentVMState()
	if err != nil {
		return nil, err
	}
	if err := agentUnavailableForVMState(state); err != nil {
		return nil, err
	}

	// Fast path: verify existing connection.
	s.agentMu.RLock()
	if ua := s.userAgent; ua != nil {
		s.agentMu.RUnlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := ua.UserExec(ctx, []string{"/usr/bin/true"}, nil, ""); err == nil {
			return ua, nil
		}
	} else {
		s.agentMu.RUnlock()
	}

	// Slow path: connect.
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if s.userAgent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := s.userAgent.UserExec(ctx, []string{"/usr/bin/true"}, nil, ""); err == nil {
			return s.userAgent, nil
		}
		s.userAgent.Close()
		s.userAgent = nil
	}
	if err := s.connectUserAgentLocked(); err != nil {
		return nil, err
	}
	return s.userAgent, nil
}

// connectUserAgentLocked establishes the user agent connection on port 1025.
// Caller must hold s.agentMu write lock.
func (s *ControlServer) connectUserAgentLocked() error {
	if s.userAgent != nil {
		return nil
	}

	if err := s.connectUserAgentPortLocked(); err == nil {
		return nil
	} else if linuxMode {
		return err
	} else {
		repairErr := s.bootstrapUserAgentLocked()
		if repairErr != nil {
			return fmt.Errorf("%v; bootstrap user agent: %w", err, repairErr)
		}
		if retryErr := s.connectUserAgentPortLocked(); retryErr != nil {
			return fmt.Errorf("connect user agent after bootstrap: %w", retryErr)
		}
		return nil
	}
}

func (s *ControlServer) connectUserAgentPortLocked() error {
	mgr, err := NewVsockDeviceManager(s.vm, s.vmQueue)
	if err != nil {
		return fmt.Errorf("vsock device: %w", err)
	}

	conn, err := mgr.ConnectToAgent(userAgentPort)
	if err != nil {
		return fmt.Errorf("connect user agent port %d: %w (user agent may not be running; check /tmp/vz-agent-user.log inside the vm)", userAgentPort, err)
	}

	client, err := NewUserAgentClient(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("user agent client: %w", err)
	}

	s.userAgent = client
	return nil
}

func (s *ControlServer) bootstrapUserAgentLocked() error {
	if linuxMode {
		return fmt.Errorf("user agent bootstrap is only supported for macOS guests")
	}
	if err := s.connectAgentLocked(); err != nil {
		return fmt.Errorf("connect daemon agent: %w", err)
	}

	user, uid, err := s.consoleUserLocked()
	if err != nil {
		return err
	}

	plistPath := "/Library/LaunchAgents/" + agentLaunchAgentLabel + ".plist"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := s.agent.WriteFile(ctx, plistPath, []byte(agentLaunchAgentPlist), 0644); err != nil {
		return fmt.Errorf("write %s: %w", plistPath, err)
	}

	script := fmt.Sprintf(`
set -e
chown root:wheel %q 2>/dev/null || chown root:0 %q
chmod 644 %q
launchctl print gui/%d/%s >/dev/null 2>&1 && launchctl bootout gui/%d/%s >/dev/null 2>&1 || true
launchctl bootstrap gui/%d %q
launchctl enable gui/%d/%s
launchctl kickstart -k gui/%d/%s
`, plistPath, plistPath, plistPath, uid, agentLaunchAgentLabel, uid, agentLaunchAgentLabel, uid, plistPath, uid, agentLaunchAgentLabel, uid, agentLaunchAgentLabel)

	result, err := s.agent.Exec(ctx, []string{"sh", "-lc", script}, nil, "")
	if err != nil {
		return fmt.Errorf("bootstrap %s for %s (%d): %w", agentLaunchAgentLabel, user, uid, err)
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(string(result.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(result.Stdout))
		}
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("bootstrap %s for %s (%d): %s", agentLaunchAgentLabel, user, uid, msg)
	}
	return nil
}

func (s *ControlServer) consoleUserLocked() (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := s.agent.Exec(ctx, []string{"stat", "-f", "%Su %u", "/dev/console"}, nil, "")
	if err != nil {
		return "", 0, fmt.Errorf("query console user: %w", err)
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(string(result.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(result.Stdout))
		}
		if msg == "" {
			msg = "unknown error"
		}
		return "", 0, fmt.Errorf("query console user: %s", msg)
	}
	return parseConsoleOwnerOutput(string(result.Stdout))
}

func parseConsoleOwnerOutput(stdout string) (string, int, error) {
	fields := strings.Fields(strings.TrimSpace(stdout))
	if len(fields) != 2 {
		return "", 0, fmt.Errorf("unexpected console owner output %q", strings.TrimSpace(stdout))
	}
	var uid int
	if _, err := fmt.Sscanf(fields[1], "%d", &uid); err != nil {
		return "", 0, fmt.Errorf("parse console uid %q: %w", fields[1], err)
	}
	if fields[0] == "root" || uid == 0 {
		return "", 0, fmt.Errorf("no logged-in GUI user on /dev/console")
	}
	return fields[0], uid, nil
}

func (s *ControlServer) currentVMState() (vz.VZVirtualMachineState, error) {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return vz.VZVirtualMachineStateError, fmt.Errorf("vm not configured")
	}
	state := vz.VZVirtualMachineStateStopped
	DispatchSync(uintptr(s.vmQueue.Handle()), func() {
		state = vz.VZVirtualMachineState(s.vm.State())
	})
	return state, nil
}

func agentUnavailableForVMState(state vz.VZVirtualMachineState) error {
	label := vmStateLabel(state)
	switch state {
	case vz.VZVirtualMachineStateRunning:
		return nil
	case vz.VZVirtualMachineStateStarting, vz.VZVirtualMachineStateResuming, vz.VZVirtualMachineStateRestoring:
		return fmt.Errorf("guest agent unavailable: vm is %s (still booting)", label)
	case vz.VZVirtualMachineStatePaused:
		return fmt.Errorf("guest agent unavailable: vm is paused")
	default:
		return fmt.Errorf("guest agent unavailable: vm is %s", label)
	}
}

// connectAgentLocked establishes the agent connection.
// Caller must hold s.agentMu.
func (s *ControlServer) connectAgentLocked() error {
	if s.agent != nil {
		return nil // already connected
	}

	mgr, err := NewVsockDeviceManager(s.vm, s.vmQueue)
	if err != nil {
		return fmt.Errorf("vsock device: %w", err)
	}

	conn, err := mgr.ConnectToAgent(agentPort)
	if err != nil {
		return fmt.Errorf("connect agent: %w (guest may still be booting; check /var/log/vz-agent.log inside the vm)", err)
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
// Agent commands run concurrently — the lock is only held briefly for connection setup.
func (s *ControlServer) handleAgentCommand(req *controlpb.ControlRequest) (resp *controlpb.ControlResponse, ok bool) {
	if !strings.HasPrefix(req.Type, "agent-") {
		return nil, false
	}

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
	case "agent-user-exec":
		cmd := req.GetAgentExec()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing agent-exec command payload"}, true
		}
		return s.handleAgentUserExec(cmd), true
	case "agent-mount-volumes":
		return s.handleAgentMountVolumes(), true
	case "agent-status":
		return s.handleAgentStatus(), true
	default:
		return nil, false
	}
}

func (s *ControlServer) handleAgentUserExec(cmd *controlpb.AgentExecCommand) *controlpb.ControlResponse {
	ua, err := s.getUserAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("user agent: %v", err)}
	}
	if len(cmd.Args) == 0 {
		return &controlpb.ControlResponse{Error: "args required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := ua.UserExec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("user exec: %v", err)}
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

func (s *ControlServer) handleAgentConnect() *controlpb.ControlResponse {
	// Force reconnect: write lock to close and reopen.
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if s.agent != nil {
		s.agent.Close()
		s.agent = nil
	}
	if err := s.connectAgentLocked(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return &controlpb.ControlResponse{Success: true, Data: "connected to guest agent", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "connected to guest agent"}}}
}

func (s *ControlServer) handleAgentStatus() *controlpb.ControlResponse {
	s.healthMu.RLock()
	h := s.agentHealth
	s.healthMu.RUnlock()

	status := map[string]any{
		"daemon":   h.daemonStatus,
		"user":     h.userStatus,
		"lastPing": h.lastPing.Format(time.RFC3339),
		"version":  h.version,
	}
	if h.lastErr != "" {
		status["lastError"] = h.lastErr
	}
	if !h.lastPing.IsZero() {
		status["ago"] = time.Since(h.lastPing).Round(time.Second).String()
	}

	data, _ := json.Marshal(status)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: string(data)}},
	}
}

// agentHealthMonitor runs in the background, pinging the agent every 10 seconds.
// On failure, it marks the agent as disconnected and attempts reconnection with backoff.
func (s *ControlServer) agentHealthMonitor() {
	// Wait a bit for the VM to boot before starting health checks.
	time.Sleep(5 * time.Second)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	failCount := 0
	for {
		if !s.running.Load() {
			return
		}

		s.healthCheckOnce(&failCount)

		<-ticker.C
	}
}

func (s *ControlServer) healthCheckOnce(failCount *int) {
	state, err := s.currentVMState()
	if err != nil || agentUnavailableForVMState(state) != nil {
		s.setHealthStatus("disconnected", "", "vm not running")
		return
	}

	// Try to ping via existing connection (read lock only).
	s.agentMu.RLock()
	a := s.agent
	s.agentMu.RUnlock()

	if a != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		agentVer, err := a.Ping(ctx)
		cancel()
		if err == nil {
			*failCount = 0
			s.setHealthStatus("connected", agentVer, "")
			s.checkAgentVersion(agentVer)
			s.healthCheckUserAgent()
			return
		}
	}

	// Ping failed or no connection. Attempt reconnect.
	*failCount++
	s.setHealthStatus("reconnecting", "", fmt.Sprintf("ping failed (attempt %d)", *failCount))

	s.agentMu.Lock()
	if s.agent != nil {
		s.agent.Close()
		s.agent = nil
	}
	err = s.connectAgentLocked()
	s.agentMu.Unlock()

	if err != nil {
		s.setHealthStatus("disconnected", "", err.Error())
		return
	}

	// Reconnected — verify with a ping.
	s.agentMu.RLock()
	a = s.agent
	s.agentMu.RUnlock()

	if a != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		agentVer, err := a.Ping(ctx)
		cancel()
		if err == nil {
			*failCount = 0
			s.setHealthStatus("connected", agentVer, "")
			log.Printf("agent-health: reconnected (version %s)", agentVer)
			s.checkAgentVersion(agentVer)
			s.healthCheckUserAgent()
			return
		}
		s.setHealthStatus("disconnected", "", fmt.Sprintf("reconnected but ping failed: %v", err))
	}
}

// checkAgentVersion compares the guest agent version with the host version.
// Logs a warning on mismatch. If autoUpgradeAgent is enabled and this is the
// first mismatch detected, triggers a background upgrade.
func (s *ControlServer) checkAgentVersion(agentVer string) {
	s.healthMu.RLock()
	checked := s.agentHealth.versionChecked
	s.healthMu.RUnlock()
	if checked {
		return
	}

	s.healthMu.Lock()
	s.agentHealth.versionChecked = true
	s.healthMu.Unlock()

	hostVer := hostVersion()

	// Skip comparison if either version is unresolvable.
	if agentVer == "" || agentVer == "dev" || agentVer == "unknown" {
		return
	}
	if hostVer == "" || hostVer == "dev" || hostVer == "unknown" {
		return
	}

	if agentVer == hostVer {
		log.Printf("agent-health: version match (%s)", agentVer)
		return
	}

	log.Printf("agent-health: version mismatch: host=%s guest=%s", hostVer, agentVer)

	if !autoUpgradeAgent {
		log.Printf("agent-health: run 'vz-macos agent-upgrade' to update, or use -auto-upgrade-agent")
		return
	}

	s.healthMu.RLock()
	attempted := s.agentHealth.upgradeAttempted
	s.healthMu.RUnlock()
	if attempted {
		return
	}

	s.healthMu.Lock()
	s.agentHealth.upgradeAttempted = true
	s.healthMu.Unlock()

	log.Printf("agent-health: auto-upgrading agent (%s -> %s)...", agentVer, hostVer)
	go func() {
		if err := upgradeAgent(); err != nil {
			log.Printf("agent-health: auto-upgrade failed: %v", err)
			return
		}
		// Reset version check so next ping verifies the new version.
		s.healthMu.Lock()
		s.agentHealth.versionChecked = false
		s.healthMu.Unlock()
		log.Printf("agent-health: auto-upgrade complete")
	}()
}

func (s *ControlServer) healthCheckUserAgent() {
	ua, err := s.getUserAgent()
	if err != nil {
		s.healthMu.Lock()
		s.agentHealth.userStatus = "disconnected"
		s.healthMu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err = ua.UserExec(ctx, []string{"true"}, nil, "")
	cancel()
	s.healthMu.Lock()
	if err == nil {
		s.agentHealth.userStatus = "connected"
	} else {
		s.agentHealth.userStatus = "disconnected"
	}
	s.healthMu.Unlock()
}

// AgentHealthSummary returns a short status string for UI display.
// Thread-safe; intended for main-thread polling.
func (s *ControlServer) AgentHealthSummary() string {
	s.healthMu.RLock()
	h := s.agentHealth
	s.healthMu.RUnlock()

	switch h.daemonStatus {
	case "connected":
		if h.userStatus == "connected" {
			return "Agent: connected"
		}
		return "Agent: connected (no user session)"
	case "reconnecting":
		return "Agent: reconnecting..."
	case "disconnected":
		if h.lastPing.IsZero() {
			// Never connected — still booting up.
			return "Agent: connecting..."
		}
		return "Agent: disconnected"
	default:
		// No health check has run yet.
		return "Agent: connecting..."
	}
}

func (s *ControlServer) setHealthStatus(status, version, lastErr string) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.agentHealth.daemonStatus = status
	if version != "" {
		s.agentHealth.version = version
	}
	s.agentHealth.lastErr = lastErr
	if status == "connected" {
		s.agentHealth.lastPing = time.Now()
	}
}

func (s *ControlServer) handleAgentPing() *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := a.Ping(ctx)
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
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := a.Info(ctx)
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
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if len(cmd.Args) == 0 {
		return &controlpb.ControlResponse{Error: "args required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := a.Exec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
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
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if cmd.Path == "" {
		return &controlpb.ControlResponse{Error: "path required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := a.ReadFile(ctx, cmd.Path)
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
	a, err := s.getAgent()
	if err != nil {
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
	if err := a.WriteFile(ctx, cmd.Path, data, mode); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("write: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "ok", Result: &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: "ok"}}}
}

func (s *ControlServer) handleAgentShutdown(cmd *controlpb.AgentShutdownCommand) *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx, cmd.Force); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("shutdown: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "shutdown initiated", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "shutdown initiated"}}}
}

func (s *ControlServer) handleAgentReboot() *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Reboot(ctx); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("reboot: %v", err)}
	}
	return &controlpb.ControlResponse{Success: true, Data: "reboot initiated", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "reboot initiated"}}}
}

func (s *ControlServer) handleAgentSSHD(cmd *controlpb.AgentSSHDCommand) *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
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
	result, err := a.Exec(ctx, args, nil, "")
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
	a, err := s.getAgent()
	if err != nil {
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
		a.Exec(ctx, []string{"mkdir", "-p", mountPoint}, nil, "")
		cancel()

		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		result, err := a.Exec(ctx, []string{"mount_virtiofs", m.Tag, mountPoint}, nil, "")
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

	return agentMountVolumesResponse(results)
}

func agentMountVolumesResponse(results []map[string]interface{}) *controlpb.ControlResponse {
	success := true
	for _, entry := range results {
		if _, failed := entry["error"]; failed {
			success = false
			break
		}
	}
	data, _ := json.Marshal(results)
	return &controlpb.ControlResponse{
		Success: success,
		Data:    string(data),
		Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: string(data)}},
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var stream ExecStreamReceiver
	var connErr error
	if req.Type == "agent-user-exec-stream" {
		ua, err := s.getUserAgent()
		if err != nil {
			writeResponse(conn, &controlpb.ControlResponse{Error: err.Error()})
			return
		}
		stream, connErr = ua.UserExecStream(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	} else {
		a, err := s.getAgent()
		if err != nil {
			writeResponse(conn, &controlpb.ControlResponse{Error: err.Error()})
			return
		}
		stream, connErr = a.ExecStream(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	}
	err := connErr
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
		"exitCode": finalExitCode,
	})
	writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(donePayload)})
}

func (s *ControlServer) handleAgentCopy(cmd *controlpb.AgentCopyCommand) *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if cmd.HostPath == "" || cmd.GuestPath == "" {
		return &controlpb.ControlResponse{Error: "host_path and guest_path required"}
	}

	timeout := 10 * time.Minute
	if cmd.ToGuest {
		info, err := os.Stat(cmd.HostPath)
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("stat %s: %v", cmd.HostPath, err)}
		}

		// Directory copy: use longer timeout (large apps like Xcode can be 3+ GB).
		if info.IsDir() {
			timeout = 30 * time.Minute
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if cmd.ToGuest {
		info, _ := os.Stat(cmd.HostPath) // already checked above

		// Directory copy: tar on host, stream to guest, extract there.
		if info.IsDir() {
			return s.handleAgentCopyDir(ctx, a, cmd.HostPath, cmd.GuestPath, cmd.Overwrite)
		}

		mode := os.FileMode(cmd.Mode)
		if mode == 0 {
			mode = info.Mode()
		}
		if err := a.CopyToGuest(ctx, cmd.HostPath, cmd.GuestPath, mode); err != nil {
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
	if err := a.CopyFromGuest(ctx, cmd.GuestPath, cmd.HostPath); err != nil {
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

// handleAgentCopyDir copies a host directory to the guest by streaming a tar
// archive over the CopyIn RPC, then extracting it on the guest side.
// If overwrite is false and the destination already exists, the copy is skipped.
func (s *ControlServer) handleAgentCopyDir(ctx context.Context, a *AgentClient, hostDir, guestDir string, overwrite bool) *controlpb.ControlResponse {
	// Skip if destination already exists (unless overwrite is set).
	if !overwrite {
		checkResult, _ := a.Exec(ctx, []string{"test", "-d", guestDir}, nil, "")
		if checkResult != nil && checkResult.ExitCode == 0 {
			sizeResult, _ := a.Exec(ctx, []string{"du", "-sh", guestDir}, nil, "")
			sizeStr := ""
			if sizeResult != nil {
				sizeStr = strings.TrimSpace(string(sizeResult.Stdout))
			}
			msg := fmt.Sprintf("%s -> guest:%s (already exists, %s)", hostDir, guestDir, sizeStr)
			return &controlpb.ControlResponse{
				Success: true,
				Data:    msg,
				Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: msg}},
			}
		}
	}

	// Stream tar from host dir into a temp file on guest.
	tmpTar := "/tmp/vz-cp-" + filepath.Base(hostDir) + ".tar"

	// Always clean up the temp tar, even on failure.
	defer func() {
		a.Exec(context.Background(), []string{"rm", "-f", tmpTar}, nil, "")
	}()

	pr, pw := io.Pipe()

	// Tar the directory in a goroutine.
	go func() {
		cmd := exec.Command("tar", "cf", "-", "-C", filepath.Dir(hostDir), filepath.Base(hostDir))
		cmd.Stdout = pw
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			pw.CloseWithError(fmt.Errorf("tar: %w", err))
			return
		}
		pw.Close()
	}()

	// Stream the tar to guest via CopyIn.
	if err := a.CopyReaderToGuest(ctx, pr, tmpTar, 0644); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("stream tar to guest: %v", err)}
	}

	// Extract on guest.
	result, err := a.Exec(ctx, []string{"tar", "xf", tmpTar, "-C", filepath.Dir(guestDir)}, nil, "")
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("extract tar on guest: %v", err)}
	}
	if result.ExitCode != 0 {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("extract tar: exit %d: %s", result.ExitCode, string(result.Stderr))}
	}

	// Check extracted size.
	sizeResult, _ := a.Exec(ctx, []string{"du", "-sh", guestDir}, nil, "")
	sizeStr := ""
	if sizeResult != nil {
		sizeStr = strings.TrimSpace(string(sizeResult.Stdout))
	}

	msg := fmt.Sprintf("%s -> guest:%s (%s)", hostDir, guestDir, sizeStr)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    msg,
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: msg}},
	}
}
