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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	vz "github.com/tmc/apple/virtualization"
	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/controlserver"
	"github.com/tmc/cove/internal/vmconfig"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

// getAgent / getUserAgent / consoleUser are thin forwarders that
// delegate to the agent bridge sub-component.
func (s *ControlServer) getAgent() (*agentstate.AgentClient, error) {
	return s.bridge.GetAgent()
}

func (s *ControlServer) getUserAgent() (*agentstate.UserAgentClient, error) {
	return s.bridge.GetUserAgent()
}

func (s *ControlServer) consoleUser() (string, int, error) {
	if s.consoleUserOverride != nil {
		return s.consoleUserOverride()
	}
	return s.bridge.ConsoleUser()
}

// -- AgentHost interface implementation -------------------------------

// VMDir returns the active VM directory.
func (s *ControlServer) VMDir() string { return s.vmDir }

// VMState reports the current VZVirtualMachine state.
func (s *ControlServer) VMState() (vz.VZVirtualMachineState, error) {
	return s.currentVMState()
}

// Linux reports whether the guest is a Linux VM.
func (s *ControlServer) Linux() bool { return linuxMode }

// Now returns the current time, routed through the lifecycle clock so
// tests can stub it.
func (s *ControlServer) Now() time.Time { return vmLifecycleClock.Now() }

// DialAgent opens a vsock connection to the guest agent on port.
func (s *ControlServer) DialAgent(ctx context.Context, port uint32) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mgr, err := NewVsockDeviceManager(s.vm, s.vmQueue)
	if err != nil {
		return nil, fmt.Errorf("vsock device: %w", err)
	}
	return mgr.ConnectToAgent(port)
}

// LifecycleContext returns the active lifecycle context.
func (s *ControlServer) LifecycleContext() context.Context { return s.lifecycleContext() }

// Running reports whether the control server's VM run flag is live.
func (s *ControlServer) Running() bool { return s.running.Load() }

// TimeoutContext returns a context derived from the lifecycle context
// with the given timeout.
func (s *ControlServer) TimeoutContext(d time.Duration) (context.Context, context.CancelFunc) {
	return s.timeoutContext(d)
}

// ProbeGUISession dispatches the GUI-session probe by guest OS.
func (s *ControlServer) ProbeGUISession(ctx context.Context, a *agentstate.AgentClient) (controlserver.GUISession, bool, error) {
	switch agentstate.Platform(s.vmDir) {
	case agentstate.PlatformLinux:
		return probeLinuxGUISession(ctx, a)
	case agentstate.PlatformMacOS:
		return probeMacOSGUISession(ctx, a)
	default:
		return controlserver.GUISession{}, false, nil
	}
}

// LaunchAgentArtifact returns the launchd label and plist used when
// bootstrapping the per-user launch agent on macOS guests.
func (s *ControlServer) LaunchAgentArtifact() (string, string) {
	return agentLaunchAgentLabel, agentLaunchAgentPlist
}

func (s *ControlServer) shouldLogVersionMismatch(key string) bool {
	if s == nil {
		return true
	}
	s.versionWarnMu.Lock()
	defer s.versionWarnMu.Unlock()
	if s.warnedVersions == nil {
		s.warnedVersions = make(map[string]bool)
	}
	if s.warnedVersions[key] {
		return false
	}
	s.warnedVersions[key] = true
	return true
}

// MaybeAutoUpgradeAgent compares the guest agent version with the
// host version and triggers an in-place upgrade in a goroutine when
// the policy allows. Returns true when an upgrade was kicked off.
func (s *ControlServer) MaybeAutoUpgradeAgent(agentVer string, onUpgraded func()) bool {
	hostVer := hostVersion()

	switch agentstate.CompareVersions(hostVer, agentVer) {
	case agentstate.VersionUnknown:
		return false
	case agentstate.VersionEqual:
		log.Printf("agent-health: version match (%s)", agentVer)
		return false
	case agentstate.VersionGuestNewer:
		log.Printf("agent-health: guest agent %s is newer than host %s; not downgrading", agentVer, hostVer)
		return false
	case agentstate.VersionGuestOlder, agentstate.VersionDifferent:
		// fall through to upgrade path
	}

	key := hostVer + ":" + agentVer
	firstWarning := s.shouldLogVersionMismatch(key)
	if firstWarning {
		log.Printf("agent-health: version mismatch: host=%s guest=%s", hostVer, agentVer)
	}

	if !sandboxAllowsAgentUpgrade() {
		if firstWarning {
			log.Printf("agent-health: run 'cove agent-upgrade' to update, or use -auto-upgrade-agent")
		}
		return false
	}

	log.Printf("agent-health: auto-upgrading agent (%s -> %s)...", agentVer, hostVer)
	go func() {
		if err := upgradeAgent(); err != nil {
			log.Printf("agent-health: auto-upgrade failed: %v", err)
			return
		}
		if onUpgraded != nil {
			onUpgraded()
		}
		log.Printf("agent-health: auto-upgrade complete")
	}()
	return true
}

// -- ControlServer-side helpers ---------------------------------------

func responseText(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return strings.ToValidUTF8(string(data), "�")
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
	case "agent-exec", "agent-exec-auto":
		cmd := req.GetAgentExec()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing agent-exec command payload"}, true
		}
		if req.Type == "agent-exec-auto" && agentstate.RouteForExec(cmd.Args, linuxMode) == agentstate.RouteUser {
			slog.Debug(fmt.Sprintf("agent-route: exec %v -> user agent", cmd.Args))
			return s.handleAgentUserExec(cmd), true
		}
		return s.handleAgentExec(cmd), true
	case "agent-exec-stream":
		return &controlpb.ControlResponse{
			Error: "agent-exec-stream requires streaming transport (use one request per connection)",
		}, true
	case "agent-exec-attach":
		return &controlpb.ControlResponse{
			Error: "agent-exec-attach requires streaming transport (use one request per connection)",
		}, true
	case "agent-exec-resize", "agent-exec-signal":
		return &controlpb.ControlResponse{
			Error: fmt.Sprintf("%s expects raw JSON dispatch; use the unix control socket", req.Type),
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
	s.notePolicyExec()
	ua, err := s.getUserAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("user agent unavailable: finish logging into the VM and retry: %v", err)}
	}
	if len(cmd.Args) == 0 {
		return &controlpb.ControlResponse{Error: "args required"}
	}
	ctx, cancel := s.timeoutContext(10 * time.Minute)
	defer cancel()
	result, err := ua.UserExec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("user exec: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"exitCode": result.ExitCode,
		"stdout":   responseText(result.Stdout),
		"stderr":   responseText(result.Stderr),
		"duration": result.DurationSeconds,
	})
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
			ExitCode:        result.ExitCode,
			Stdout:          responseText(result.Stdout),
			Stderr:          responseText(result.Stderr),
			DurationSeconds: result.DurationSeconds,
		}},
	}
}

func (s *ControlServer) handleAgentConnect() *controlpb.ControlResponse {
	if err := s.bridge.ForceReconnect(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	return &controlpb.ControlResponse{Success: true, Data: "connected to guest agent", Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "connected to guest agent"}}}
}

func (s *ControlServer) handleAgentStatus() *controlpb.ControlResponse {
	h := s.bridge.HealthSnapshot()

	status := map[string]any{
		"daemon":   h.DaemonStatus,
		"user":     h.UserStatus,
		"lastPing": h.LastPing.Format(time.RFC3339),
		"summary":  controlserver.AgentHealthSummary(h),
		"version":  h.Version,
	}
	if h.GUISessionActive {
		status["guiSession"] = map[string]string{
			"user": h.GUISession.User,
			"seat": h.GUISession.Seat,
			"type": h.GUISession.Kind,
		}
	}
	if h.LastErr != "" {
		status["lastError"] = h.LastErr
	}
	if !h.LastPing.IsZero() {
		status["ago"] = time.Since(h.LastPing).Round(time.Second).String()
	}

	data, _ := json.Marshal(status)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: string(data)}},
	}
}

// defaultAgentHealthInterval is the tick cadence the agent health monitor
// uses when COVE_AGENT_HEALTH_INTERVAL is unset or unparseable.
const defaultAgentHealthInterval = 30 * time.Second

// agentHealthIntervalEnv lets operators override the tick cadence at boot
// time.
const agentHealthIntervalEnv = "COVE_AGENT_HEALTH_INTERVAL"

// resolveAgentHealthInterval returns the tick cadence for the agent health
// monitor.
func resolveAgentHealthInterval() time.Duration {
	raw := os.Getenv(agentHealthIntervalEnv)
	if raw == "" {
		return defaultAgentHealthInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("agent-health: ignoring unparseable interval %q for %s, falling back to %s",
			raw, agentHealthIntervalEnv, defaultAgentHealthInterval)
		return defaultAgentHealthInterval
	}
	return d
}

// agentHealthMonitor runs the proactive health monitor for the bridge.
func (s *ControlServer) agentHealthMonitor() {
	s.bridge.HealthMonitor(resolveAgentHealthInterval())
}

func (s *ControlServer) AgentHealthSummary() string { return s.bridge.Summary() }

func (s *ControlServer) markAgentConnected(version string) { s.bridge.MarkAgentConnected(version) }

func (s *ControlServer) handleAgentPing() *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := s.timeoutContext(5 * time.Second)
	defer cancel()
	version, err := a.Ping(ctx)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("ping: %v", err)}
	}
	s.markAgentConnected(version)
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
	ctx, cancel := s.timeoutContext(10 * time.Second)
	defer cancel()
	info, err := a.Info(ctx)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("info: %v", err)}
	}
	if vmDirectory := s.effectiveVMDir(); strings.TrimSpace(vmDirectory) != "" {
		if markErr := agentstate.MarkVerifiedInfo(vmDirectory, agentstate.DetectPlatform(vmDirectory), agentstate.SourceRuntime, info.GetAgentVersion(), info.GetAgentCommit(), normalizeAgentFeatures(info.GetFeatures()), time.Now()); markErr != nil && verbose {
			fmt.Printf("warning: record guest agent info: %v\n", markErr)
		}
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
	s.notePolicyExec()
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	if len(cmd.Args) == 0 {
		return &controlpb.ControlResponse{Error: "args required"}
	}
	ctx, cancel := s.timeoutContext(10 * time.Minute)
	defer cancel()
	result, err := a.Exec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("exec: %v", err)}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"exitCode": result.ExitCode,
		"stdout":   responseText(result.Stdout),
		"stderr":   responseText(result.Stderr),
		"duration": result.DurationSeconds,
	})
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
			ExitCode:        result.ExitCode,
			Stdout:          responseText(result.Stdout),
			Stderr:          responseText(result.Stderr),
			DurationSeconds: result.DurationSeconds,
		}},
	}
}

func (s *ControlServer) handleAgentRead(cmd *controlpb.AgentFileReadCommand) *controlpb.ControlResponse {
	if cmd.Path == "" {
		return &controlpb.ControlResponse{Error: "path required"}
	}
	ctx, cancel := s.timeoutContext(30 * time.Second)
	defer cancel()

	var data []byte
	var err error
	if agentstate.RouteFor("read", cmd.Path, linuxMode) == agentstate.RouteUser {
		slog.Debug(fmt.Sprintf("agent-route: read %s -> user agent (TCC path)", cmd.Path))
		data, err = s.userAgentReadFile(ctx, cmd.Path)
	} else {
		var a *agentstate.AgentClient
		a, err = s.getAgent()
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		data, err = a.ReadFile(ctx, cmd.Path)
	}
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("read: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(data),
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Data: data}},
	}
}

func (s *ControlServer) userAgentReadFile(ctx context.Context, path string) ([]byte, error) {
	ua, err := s.getUserAgent()
	if err != nil {
		return nil, fmt.Errorf("user agent: %w", err)
	}
	result, err := ua.UserExec(ctx, []string{"/usr/bin/base64", "-i", path}, nil, "")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("base64 -i %s: exit %d: %s", path, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(result.Stdout)))
	if err != nil {
		return nil, fmt.Errorf("decode user-agent base64 of %s: %w", path, err)
	}
	return decoded, nil
}

func (s *ControlServer) handleAgentWrite(cmd *controlpb.AgentFileWriteCommand) *controlpb.ControlResponse {
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
	ctx, cancel := s.timeoutContext(30 * time.Second)
	defer cancel()

	if agentstate.RouteFor("write", cmd.Path, linuxMode) == agentstate.RouteUser {
		slog.Debug(fmt.Sprintf("agent-route: write %s -> user agent (TCC path)", cmd.Path))
		if err := s.userAgentWriteFile(ctx, cmd.Path, data, mode); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("write: %v", err)}
		}
	} else {
		a, err := s.getAgent()
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		if err := a.WriteFile(ctx, cmd.Path, data, mode); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("write: %v", err)}
		}
	}
	return &controlpb.ControlResponse{Success: true, Data: "ok", Result: &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: "ok"}}}
}

func (s *ControlServer) userAgentWriteFile(ctx context.Context, path string, data []byte, mode uint32) error {
	ua, err := s.getUserAgent()
	if err != nil {
		return fmt.Errorf("user agent: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	script := `set -e; mkdir -p "$1"; printf %s "$3" | /usr/bin/base64 -d > "$2"; chmod "$4" "$2"`
	args := []string{
		"/bin/sh", "-c", script, "vz-agent-write",
		guestDir(path), path, encoded, fmt.Sprintf("%o", mode&0o777),
	}
	result, err := ua.UserExec(ctx, args, nil, "")
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("user-agent write %s: exit %d: %s", path, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

func guestDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		if i == 0 {
			return "/"
		}
		return p[:i]
	}
	return "."
}

func (s *ControlServer) handleAgentShutdown(cmd *controlpb.AgentShutdownCommand) *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	ctx, cancel := s.timeoutContext(10 * time.Second)
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
	ctx, cancel := s.timeoutContext(10 * time.Second)
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
	args, err := agentSSHDArgs(cmd.Action, linuxMode)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown sshd action: %s (use on, off, start, stop, enable, or status)", cmd.Action)}
	}
	ctx, cancel := s.timeoutContext(30 * time.Second)
	defer cancel()
	result, err := a.Exec(ctx, args, nil, "")
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("sshd %s: %v", cmd.Action, err)}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"exitCode": result.ExitCode,
		"stdout":   responseText(result.Stdout),
		"stderr":   responseText(result.Stderr),
	})
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
			ExitCode: result.ExitCode,
			Stdout:   responseText(result.Stdout),
			Stderr:   responseText(result.Stderr),
		}},
	}
}

func agentSSHDArgs(action string, linuxGuest bool) ([]string, error) {
	if linuxGuest {
		switch action {
		case "on":
			return []string{"systemctl", "enable", "--now", "ssh.service", "ssh.socket"}, nil
		case "off":
			return []string{"systemctl", "disable", "--now", "ssh.service", "ssh.socket"}, nil
		case "start":
			return []string{"systemctl", "start", "ssh.service", "ssh.socket"}, nil
		case "stop":
			return []string{"systemctl", "stop", "ssh.service", "ssh.socket"}, nil
		case "enable":
			return []string{"systemctl", "enable", "--now", "ssh.service", "ssh.socket"}, nil
		case "status":
			return []string{"systemctl", "show", "-p", "ActiveState", "--value", "ssh"}, nil
		default:
			return nil, fmt.Errorf("unknown sshd action")
		}
	}
	switch action {
	case "on":
		return []string{"launchctl", "load", "-w", "/System/Library/LaunchDaemons/ssh.plist"}, nil
	case "off":
		return []string{"launchctl", "unload", "-w", "/System/Library/LaunchDaemons/ssh.plist"}, nil
	case "status":
		return []string{"systemsetup", "-getremotelogin"}, nil
	default:
		return nil, fmt.Errorf("unknown sshd action")
	}
}

func (s *ControlServer) handleAgentMountVolumes() *controlpb.ControlResponse {
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	cfg, err := vmconfig.Load(s.effectiveVMDir())
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

		ctx, cancel := s.timeoutContext(10 * time.Second)
		a.Exec(ctx, []string{"mkdir", "-p", mountPoint}, nil, "")
		cancel()

		mountArgs := virtioFSMountArgs(m, mountPoint, linuxMode)
		ctx, cancel = s.timeoutContext(10 * time.Second)
		result, err := a.Exec(ctx, mountArgs, nil, "")
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

	ctx, cancel := s.timeoutContext(10 * time.Minute)
	defer cancel()

	var stream agentstate.ExecStreamReceiver
	var connErr error
	routeUser := req.Type == "agent-user-exec-stream" ||
		(req.Type == "agent-exec-stream-auto" && agentstate.RouteForExec(cmd.Args, linuxMode) == agentstate.RouteUser)
	if routeUser {
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

func isUserPathOrHome(path string) bool {
	if agentstate.IsUserPath(path) {
		return true
	}
	for _, prefix := range []string{"/Users/", "/home/"} {
		if strings.HasPrefix(path, prefix) {
			rest := strings.TrimPrefix(path, prefix)
			if i := strings.IndexByte(rest, '/'); i > 0 {
				user := rest[:i]
				return user != "Shared"
			}
			return rest != "" && rest != "Shared"
		}
	}
	return path == "~" || strings.HasPrefix(path, "~/")
}

// targetUserForPath returns the guest user who should own a file written
// to path, or "" if path is not in a user home. The owner comes from the
// path itself (/Users/<name>/...); only "~" paths fall back to the console
// user.
func (s *ControlServer) targetUserForPath(path string) string {
	if !isUserPathOrHome(path) {
		return ""
	}
	for _, prefix := range []string{"/Users/", "/home/"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			user, _, _ := strings.Cut(rest, "/")
			if user != "" && user != "Shared" {
				return user
			}
			return ""
		}
	}
	if u, _, err := s.consoleUser(); err == nil && u != "" {
		return u
	}
	return ""
}

// chownGuest sets the owner of path on the guest without following
// symbolic links, so a user-controlled link cannot redirect a root chown.
func chownGuest(ctx context.Context, a *agentstate.AgentClient, user, path string, recursive bool) error {
	args := []string{"chown", "-h"}
	if recursive {
		args = []string{"chown", "-hR"}
	}
	args = append(args, "--", user, path)
	result, err := a.Exec(ctx, args, nil, "")
	if err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("chown %s: exit %d: %s", path, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

func (s *ControlServer) expandGuestHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	u := s.targetUserForPath(path)
	if u == "" {
		if cu, _, err := s.consoleUser(); err == nil && cu != "" {
			u = cu
		}
	}
	base := "/Users"
	if s.Linux() {
		base = "/home"
	}
	if u != "" {
		homeDir := base + "/" + u
		if path == "~" {
			return homeDir
		}
		return homeDir + path[1:]
	}
	return path
}

func (s *ControlServer) handleAgentCopy(cmd *controlpb.AgentCopyCommand) *controlpb.ControlResponse {
	if cmd.HostPath == "" || cmd.GuestPath == "" {
		return &controlpb.ControlResponse{Error: "host_path and guest_path required"}
	}
	cmd.GuestPath = s.expandGuestHome(cmd.GuestPath)
	if cmd.ToGuest {
		if strings.HasSuffix(cmd.GuestPath, "/") {
			cmd.GuestPath = path.Join(cmd.GuestPath, filepath.Base(cmd.HostPath))
		}
	} else {
		if strings.HasSuffix(cmd.HostPath, "/") {
			cmd.HostPath = filepath.Join(cmd.HostPath, filepath.Base(cmd.GuestPath))
		}
	}
	route := agentstate.RouteFor("cp", cmd.GuestPath, linuxMode)
	if route == agentstate.RouteUser {
		slog.Debug(fmt.Sprintf("agent-route: cp %s -> user agent (TCC path)", cmd.GuestPath))
	}

	timeout := 10 * time.Minute
	var hostInfo os.FileInfo
	if cmd.ToGuest {
		info, err := os.Stat(cmd.HostPath)
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("stat %s: %v", cmd.HostPath, err)}
		}
		hostInfo = info
		if info.IsDir() {
			timeout = 30 * time.Minute
		}
	} else {
		timeout = 30 * time.Minute
	}
	a, err := s.getAgent()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	ctx, cancel := s.timeoutContext(timeout)
	defer cancel()

	if cmd.ToGuest {
		if hostInfo.IsDir() {
			return s.handleAgentCopyDir(ctx, a, cmd.HostPath, cmd.GuestPath, cmd.Overwrite)
		}

		mode := os.FileMode(cmd.Mode)
		if mode == 0 {
			mode = hostInfo.Mode()
		}

		var ua *agentstate.UserAgentClient
		if route == agentstate.RouteUser {
			if client, err := s.getUserAgent(); err == nil {
				ua = client
			}
		}

		targetUser := s.targetUserForPath(cmd.GuestPath)

		// Check if destination on guest is an existing directory
		if ua != nil {
			checkResult, _ := ua.UserExec(ctx, []string{"/bin/sh", "-c", `dest="$1"; case "$dest" in "~/"*) dest="$HOME/${dest#\~/}" ;; esac; test -d "$dest"`, "vz-cp-check-dir", cmd.GuestPath}, nil, "")
			if checkResult != nil && checkResult.ExitCode == 0 {
				cmd.GuestPath = path.Join(cmd.GuestPath, filepath.Base(cmd.HostPath))
			}
		} else {
			checkResult, _ := a.Exec(ctx, []string{"/bin/test", "-d", cmd.GuestPath}, nil, "")
			if checkResult != nil && checkResult.ExitCode == 0 {
				cmd.GuestPath = path.Join(cmd.GuestPath, filepath.Base(cmd.HostPath))
			}
		}

		if ua != nil {
			stagePath := fmt.Sprintf("/tmp/vz-cp-stage-%d-%08x", time.Now().UnixNano(), rand.Uint32())
			defer func() {
				ctxCleanup, cancel := s.timeoutContext(5 * time.Second)
				defer cancel()
				a.Exec(ctxCleanup, []string{"rm", "-f", stagePath}, nil, "")
			}()

			if err := a.CopyToGuest(ctx, cmd.HostPath, stagePath, 0644); err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("cp stage: %v", err)}
			}
			if targetUser != "" {
				if err := chownGuest(ctx, a, targetUser, stagePath, false); err != nil {
					return &controlpb.ControlResponse{Error: fmt.Sprintf("cp stage: %v", err)}
				}
			}

			destDir := guestDir(cmd.GuestPath)
			script := `set -e
destDir="$1"; case "$destDir" in "~/"*) destDir="$HOME/${destDir#\~/}" ;; esac
destPath="$2"; case "$destPath" in "~/"*) destPath="$HOME/${destPath#\~/}" ;; esac
mkdir -p "$destDir"
if ! mv -f "$3" "$destPath" 2>/dev/null; then
    cp -f "$3" "$destPath"
    rm -f "$3"
fi
chmod "$4" "$destPath"`
			args := []string{
				"/bin/sh", "-c", script, "vz-cp-file",
				destDir, cmd.GuestPath, stagePath, fmt.Sprintf("%o", mode&0o777),
			}
			result, err := ua.UserExec(ctx, args, nil, "")
			if err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
			}
			if result.ExitCode != 0 {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))}
			}
		} else {
			if err := a.CopyToGuest(ctx, cmd.HostPath, cmd.GuestPath, mode); err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
			}
			if targetUser != "" {
				if err := chownGuest(ctx, a, targetUser, cmd.GuestPath, false); err != nil {
					return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
				}
			}
		}

		msg := fmt.Sprintf("%s -> guest:%s (%d bytes)", cmd.HostPath, cmd.GuestPath, hostInfo.Size())
		return &controlpb.ControlResponse{
			Success: true,
			Data:    msg,
			Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: msg}},
		}
	}

	// Guest to host
	if strings.HasSuffix(cmd.HostPath, "/") {
		cmd.HostPath = filepath.Join(cmd.HostPath, filepath.Base(cmd.GuestPath))
	} else if fi, err := os.Stat(cmd.HostPath); err == nil && fi.IsDir() {
		cmd.HostPath = filepath.Join(cmd.HostPath, filepath.Base(cmd.GuestPath))
	}

	var ua *agentstate.UserAgentClient
	if route == agentstate.RouteUser {
		if client, err := s.getUserAgent(); err == nil {
			ua = client
		}
	}

	// Check if guest path is a directory
	var isGuestDir bool
	if ua != nil {
		checkResult, _ := ua.UserExec(ctx, []string{"/bin/sh", "-c", `src="$1"; case "$src" in "~/"*) src="$HOME/${src#\~/}" ;; esac; test -d "$src"`, "vz-cp-check-dir", cmd.GuestPath}, nil, "")
		isGuestDir = checkResult != nil && checkResult.ExitCode == 0
	} else {
		checkResult, _ := a.Exec(ctx, []string{"/bin/test", "-d", cmd.GuestPath}, nil, "")
		isGuestDir = checkResult != nil && checkResult.ExitCode == 0
	}

	if isGuestDir {
		return s.handleAgentCopyDirFromGuest(ctx, a, ua, cmd.GuestPath, cmd.HostPath, cmd.Overwrite)
	}

	if ua != nil {
		stagePath := fmt.Sprintf("/tmp/vz-cp-stage-%d-%08x", time.Now().UnixNano(), rand.Uint32())
		defer func() {
			ctxCleanup, cancel := s.timeoutContext(5 * time.Second)
			defer cancel()
			a.Exec(ctxCleanup, []string{"rm", "-f", stagePath}, nil, "")
		}()

		script := `set -e
src="$1"; case "$src" in "~/"*) src="$HOME/${src#\~/}" ;; esac
/bin/cp -p "$src" "$2"`
		result, err := ua.UserExec(ctx, []string{"/bin/sh", "-c", script, "vz-cp-read", cmd.GuestPath, stagePath}, nil, "")
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
		}
		if result.ExitCode != 0 {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))}
		}
		if err := a.CopyFromGuest(ctx, stagePath, cmd.HostPath); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
		}
	} else {
		if err := a.CopyFromGuest(ctx, cmd.GuestPath, cmd.HostPath); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
		}
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

func (s *ControlServer) handleAgentCopyDir(ctx context.Context, a *agentstate.AgentClient, hostDir, guestDir string, overwrite bool) *controlpb.ControlResponse {
	route := agentstate.RouteFor("cp", guestDir, linuxMode)
	var ua *agentstate.UserAgentClient
	if route == agentstate.RouteUser {
		slog.Debug(fmt.Sprintf("agent-route: cp dir %s -> user agent (TCC path)", guestDir))
		if client, err := s.getUserAgent(); err == nil {
			ua = client
		}
	}

	targetUser := s.targetUserForPath(guestDir)

	if !overwrite {
		if ua != nil {
			checkResult, _ := ua.UserExec(ctx, []string{"/bin/sh", "-c", `dest="$1"; case "$dest" in "~/"*) dest="$HOME/${dest#\~/}" ;; esac; test -e "$dest"`, "vz-cp-check", guestDir}, nil, "")
			if checkResult != nil && checkResult.ExitCode == 0 {
				return &controlpb.ControlResponse{
					Error: fmt.Sprintf("destination %q already exists (use -f to overwrite)", guestDir),
				}
			}
		} else {
			checkResult, _ := a.Exec(ctx, []string{"/bin/sh", "-c", `test -e "$1"`, "vz-cp-check", guestDir}, nil, "")
			if checkResult != nil && checkResult.ExitCode == 0 {
				return &controlpb.ControlResponse{
					Error: fmt.Sprintf("destination %q already exists (use -f to overwrite)", guestDir),
				}
			}
		}
	}

	tmpTar := fmt.Sprintf("/tmp/vz-cp-%s-%d-%08x.tar", filepath.Base(hostDir), time.Now().UnixNano(), rand.Uint32())

	defer func() {
		ctx, cancel := s.timeoutContext(5 * time.Second)
		defer cancel()
		a.Exec(ctx, []string{"rm", "-f", tmpTar}, nil, "")
	}()

	pr, pw := io.Pipe()

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

	if err := a.CopyReaderToGuest(ctx, pr, tmpTar, 0644); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("stream tar to guest: %v", err)}
	}

	if ua != nil {
		if targetUser != "" {
			if err := chownGuest(ctx, a, targetUser, tmpTar, false); err != nil {
				return &controlpb.ControlResponse{Error: fmt.Sprintf("stream tar to guest: %v", err)}
			}
		}
		script := `set -e
dest="$1"; case "$dest" in "~/"*) dest="$HOME/${dest#\~/}" ;; esac
mkdir -p "$dest"
tar xf "$2" --no-same-owner --strip-components=1 -C "$dest"`
		result, err := ua.UserExec(ctx, []string{"/bin/sh", "-c", script, "vz-cp-extract", guestDir, tmpTar}, nil, "")
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("extract tar on guest: %v", err)}
		}
		if result.ExitCode != 0 {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("extract tar: exit %d: %s", result.ExitCode, string(result.Stderr))}
		}
		sizeResult, _ := ua.UserExec(ctx, []string{"/bin/sh", "-c", `dest="$1"; case "$dest" in "~/"*) dest="$HOME/${dest#\~/}" ;; esac; du -sh "$dest"`, "vz-cp-size", guestDir}, nil, "")
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

	if mkResult, err := a.Exec(ctx, []string{"mkdir", "-p", guestDir}, nil, ""); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("mkdir guest dir: %v", err)}
	} else if mkResult.ExitCode != 0 {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("mkdir guest dir: exit %d: %s", mkResult.ExitCode, string(mkResult.Stderr))}
	}
	result, err := a.Exec(ctx, []string{"tar", "xf", tmpTar, "--no-same-owner", "--strip-components=1", "-C", guestDir}, nil, "")
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("extract tar on guest: %v", err)}
	}
	if result.ExitCode != 0 {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("extract tar: exit %d: %s", result.ExitCode, string(result.Stderr))}
	}

	if targetUser != "" {
		if err := chownGuest(ctx, a, targetUser, guestDir, true); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("cp: %v", err)}
		}
	}

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

func (s *ControlServer) handleAgentCopyDirFromGuest(ctx context.Context, a *agentstate.AgentClient, ua *agentstate.UserAgentClient, guestDir, hostDir string, overwrite bool) *controlpb.ControlResponse {
	if _, err := os.Stat(hostDir); err == nil {
		if !overwrite {
			return &controlpb.ControlResponse{
				Error: fmt.Sprintf("destination %q already exists (use -f to overwrite)", hostDir),
			}
		}
	}

	cleanGuestDir := path.Clean(guestDir)
	tmpGuestTar := fmt.Sprintf("/tmp/vz-cp-%s-%d-%08x.tar", path.Base(cleanGuestDir), time.Now().UnixNano(), rand.Uint32())
	defer func() {
		ctxCleanup, cancel := s.timeoutContext(5 * time.Second)
		defer cancel()
		a.Exec(ctxCleanup, []string{"rm", "-f", tmpGuestTar}, nil, "")
	}()

	if ua != nil {
		script := `set -e
src="$1"; case "$src" in "~/"*) src="$HOME/${src#\~/}" ;; esac
dir=$(dirname "$src")
base=$(basename "$src")
tar cf "$2" -C "$dir" "$base"`
		result, err := ua.UserExec(ctx, []string{"/bin/sh", "-c", script, "vz-cp-tar", cleanGuestDir, tmpGuestTar}, nil, "")
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("tar guest dir: %v", err)}
		}
		if result.ExitCode != 0 {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("tar guest dir: exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))}
		}
	} else {
		gDir := path.Dir(cleanGuestDir)
		gBase := path.Base(cleanGuestDir)
		result, err := a.Exec(ctx, []string{"tar", "cf", tmpGuestTar, "-C", gDir, gBase}, nil, "")
		if err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("tar guest dir: %v", err)}
		}
		if result.ExitCode != 0 {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("tar guest dir: exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))}
		}
	}

	if err := os.MkdirAll(hostDir, 0755); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("mkdir host dir: %v", err)}
	}

	pr, pw := io.Pipe()
	extractErrCh := make(chan error, 1)
	go func() {
		cmd := exec.CommandContext(ctx, "tar", "xf", "-", "--strip-components=1", "-C", hostDir)
		cmd.Stdin = pr
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			extractErrCh <- fmt.Errorf("tar extract: %w: %s", err, strings.TrimSpace(stderr.String()))
			return
		}
		extractErrCh <- nil
	}()

	copyErr := a.CopyWriterFromGuest(ctx, tmpGuestTar, pw)
	if copyErr != nil {
		pw.CloseWithError(copyErr)
	} else {
		pw.Close()
	}
	extractErr := <-extractErrCh
	if copyErr != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("stream tar from guest: %v", copyErr)}
	}
	if extractErr != nil {
		return &controlpb.ControlResponse{Error: extractErr.Error()}
	}

	msg := fmt.Sprintf("guest:%s -> %s", guestDir, hostDir)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    msg,
		Result:  &controlpb.ControlResponse_AgentFile{AgentFile: &controlpb.AgentFileResponse{Message: msg}},
	}
}
