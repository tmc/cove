// AgentBridge owns the agent clients, the connection mutex protecting
// them, and the proactive health-monitor state. It is the agent
// sub-component of the ControlServer facade.
//
// Per design 039 §7 (facade-late rule), the bridge takes a narrow
// AgentHost interface rather than a back-reference to the facade.
package controlserver

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	vz "github.com/tmc/apple/virtualization"
	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmstate"
)

// AgentBridge holds the agent clients and health state used by the
// ControlServer. The mu/healthMu pair preserves the prior locking
// shape: mu protects connection setup of agent and userAgent;
// healthMu protects the proactive health-monitor record. Splitting
// them keeps RPC fast paths from blocking on health writes.
//
// Construct with NewAgentBridge; the zero value is also usable for
// tests that don't call host methods.
type AgentBridge struct {
	host AgentHost

	mu        sync.RWMutex                // protects agent connection setup; RLock for concurrent RPCs
	agent     *agentstate.AgentClient     // client to guest daemon agent (nil until connected)
	userAgent *agentstate.UserAgentClient // client to guest user agent (nil until connected)

	healthMu sync.RWMutex
	health   AgentHealthState
}

// SetHost wires the host onto a zero-value bridge held by value.
// ControlServer constructors call this once during init so existing
// &ControlServer{} test patterns keep compiling without explicit
// bridge construction.
func (b *AgentBridge) SetHost(host AgentHost) {
	b.host = host
}

// LastPing returns the time of the last successful agent health ping,
// or the zero time if no ping has succeeded yet. Safe for concurrent
// use.
func (b *AgentBridge) LastPing() time.Time {
	b.healthMu.RLock()
	defer b.healthMu.RUnlock()
	return b.health.LastPing
}

// HealthSnapshot returns a copy of the current agent health state.
// Safe for concurrent use.
func (b *AgentBridge) HealthSnapshot() AgentHealthState {
	b.healthMu.RLock()
	defer b.healthMu.RUnlock()
	return b.health
}

// CurrentDaemonClient returns the cached daemon agent client without
// connecting. May be nil. Used by host integration that needs to
// thread the live client through host-side code paths.
func (b *AgentBridge) CurrentDaemonClient() *agentstate.AgentClient {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.agent
}

// currentVMState routes through the host. A nil host returns the
// pre-extraction "vm not configured" error so zero-value bridges in
// tests behave the same way they did when the bridge was held by a
// not-yet-initialized ControlServer.
func (b *AgentBridge) currentVMState() (vz.VZVirtualMachineState, error) {
	if b.host == nil {
		return vz.VZVirtualMachineStateError, fmt.Errorf("vm not configured")
	}
	return b.host.VMState()
}

// timeoutContext routes through the host. A nil host falls back to
// context.Background-derived timeouts for zero-value bridges in tests.
func (b *AgentBridge) timeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if b.host == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	return b.host.TimeoutContext(timeout)
}

// AgentUnavailableForVMState reports whether the agent is reachable
// for the given VM state, returning a descriptive error if not.
func AgentUnavailableForVMState(state vz.VZVirtualMachineState) error {
	switch state {
	case vz.VZVirtualMachineStateRunning:
		return nil
	case vz.VZVirtualMachineStateStarting, vz.VZVirtualMachineStateResuming, vz.VZVirtualMachineStateRestoring:
		return fmt.Errorf("guest agent unavailable: vm is %s (still booting)", vmstate.Label(state))
	case vz.VZVirtualMachineStatePaused:
		return fmt.Errorf("guest agent unavailable: vm is paused")
	default:
		return fmt.Errorf("guest agent unavailable: vm is %s", vmstate.Label(state))
	}
}

// GetAgent returns the current daemon agent client, connecting if
// necessary.
func (b *AgentBridge) GetAgent() (*agentstate.AgentClient, error) {
	state, err := b.currentVMState()
	if err != nil {
		return nil, err
	}
	if err := AgentUnavailableForVMState(state); err != nil {
		return nil, err
	}

	// Fast path: read lock to check existing connection.
	b.mu.RLock()
	if a := b.agent; a != nil {
		b.mu.RUnlock()
		ctx, cancel := b.timeoutContext(2 * time.Second)
		defer cancel()
		if _, err := a.Ping(ctx); err == nil {
			return a, nil
		}
	} else {
		b.mu.RUnlock()
	}

	// Slow path: write lock to reconnect.
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		ctx, cancel := b.timeoutContext(2 * time.Second)
		defer cancel()
		if _, err := b.agent.Ping(ctx); err == nil {
			return b.agent, nil
		}
		b.agent.Close()
		b.agent = nil
	}
	if err := b.connectAgentLocked(); err != nil {
		return nil, err
	}
	return b.agent, nil
}

// GetUserAgent returns the user-session agent client, connecting if
// necessary. On macOS guests, missing LaunchAgents are bootstrapped
// on demand.
func (b *AgentBridge) GetUserAgent() (*agentstate.UserAgentClient, error) {
	state, err := b.currentVMState()
	if err != nil {
		return nil, err
	}
	if err := AgentUnavailableForVMState(state); err != nil {
		return nil, err
	}

	b.mu.RLock()
	if ua := b.userAgent; ua != nil {
		b.mu.RUnlock()
		ctx, cancel := b.timeoutContext(2 * time.Second)
		defer cancel()
		if _, err := ua.UserExec(ctx, []string{"/usr/bin/true"}, nil, ""); err == nil {
			return ua, nil
		}
	} else {
		b.mu.RUnlock()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.userAgent != nil {
		ctx, cancel := b.timeoutContext(2 * time.Second)
		defer cancel()
		if _, err := b.userAgent.UserExec(ctx, []string{"/usr/bin/true"}, nil, ""); err == nil {
			return b.userAgent, nil
		}
		b.userAgent.Close()
		b.userAgent = nil
	}
	if err := b.connectUserAgentLocked(); err != nil {
		return nil, err
	}
	return b.userAgent, nil
}

// connectUserAgentLocked establishes the user agent connection on
// port 1025. Caller must hold b.mu write lock.
func (b *AgentBridge) connectUserAgentLocked() error {
	if b.userAgent != nil {
		return nil
	}
	if err := b.connectUserAgentPortLocked(); err == nil {
		return nil
	} else if b.host != nil && b.host.Linux() {
		return err
	} else {
		repairErr := b.bootstrapUserAgentLocked()
		if repairErr != nil {
			return fmt.Errorf("%v; bootstrap user agent: %w", err, repairErr)
		}
		if retryErr := b.connectUserAgentPortLocked(); retryErr != nil {
			return fmt.Errorf("connect user agent after bootstrap: %w", retryErr)
		}
		return nil
	}
}

func (b *AgentBridge) connectUserAgentPortLocked() error {
	client, err := agentstate.NewUserAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := b.host.DialAgent(ctx, agentstate.UserPort)
		if err != nil {
			return nil, fmt.Errorf("connect user agent port %d: %w (user agent may not be running; check /tmp/vz-agent-user.log inside the vm)", agentstate.UserPort, err)
		}
		return conn, nil
	})
	if err != nil {
		return fmt.Errorf("user agent client: %w", err)
	}
	ctx, cancel := b.timeoutContext(5 * time.Second)
	defer cancel()
	if _, err := client.UserExec(ctx, []string{"/usr/bin/true"}, nil, ""); err != nil {
		client.Close()
		return err
	}
	b.userAgent = client
	return nil
}

func (b *AgentBridge) bootstrapUserAgentLocked() error {
	if b.host == nil {
		return fmt.Errorf("user agent bootstrap: no host")
	}
	if b.host.Linux() {
		return fmt.Errorf("user agent bootstrap is only supported for macOS guests")
	}
	if err := b.connectAgentLocked(); err != nil {
		return fmt.Errorf("connect daemon agent: %w", err)
	}

	user, uid, err := b.consoleUserLocked()
	if err != nil {
		return err
	}

	label, plist := b.host.LaunchAgentArtifact()
	plistPath := "/Library/LaunchAgents/" + label + ".plist"
	ctx, cancel := b.timeoutContext(20 * time.Second)
	defer cancel()

	if err := b.agent.WriteFile(ctx, plistPath, []byte(plist), 0644); err != nil {
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
`, plistPath, plistPath, plistPath, uid, label, uid, label, uid, plistPath, uid, label, uid, label)

	result, err := b.agent.Exec(ctx, []string{"sh", "-lc", script}, nil, "")
	if err != nil {
		return fmt.Errorf("bootstrap %s for %s (%d): %w", label, user, uid, err)
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(string(result.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(result.Stdout))
		}
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("bootstrap %s for %s (%d): %s", label, user, uid, msg)
	}
	return nil
}

func (b *AgentBridge) consoleUserLocked() (string, int, error) {
	ctx, cancel := b.timeoutContext(5 * time.Second)
	defer cancel()

	result, err := b.agent.Exec(ctx, []string{"stat", "-f", "%Su %u", "/dev/console"}, nil, "")
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
	return ParseConsoleOwnerOutput(string(result.Stdout))
}

// ConsoleUser returns the active console user, connecting the daemon
// agent if needed.
func (b *AgentBridge) ConsoleUser() (string, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.connectAgentLocked(); err != nil {
		return "", 0, fmt.Errorf("connect daemon agent: %w", err)
	}
	return b.consoleUserLocked()
}

// ParseConsoleOwnerOutput is the package-private parser for the
// `stat -f "%Su %u" /dev/console` output the macOS console-user query
// produces.
func ParseConsoleOwnerOutput(stdout string) (string, int, error) {
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

// connectAgentLocked establishes the daemon agent connection. Caller
// must hold b.mu.
func (b *AgentBridge) connectAgentLocked() error {
	if b.agent != nil {
		return nil
	}

	client, err := agentstate.NewAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := b.host.DialAgent(ctx, agentstate.DaemonPort)
		if err != nil {
			return nil, fmt.Errorf("connect agent: %w (guest may still be booting; check /var/log/vz-agent.log inside the vm)", err)
		}
		return conn, nil
	})
	if err != nil {
		return fmt.Errorf("agent client: %w", err)
	}
	ctx, cancel := b.timeoutContext(5 * time.Second)
	defer cancel()
	if _, err := client.Ping(ctx); err != nil {
		client.Close()
		return err
	}
	b.agent = client
	return nil
}

// ForceReconnect drops any cached daemon agent connection and dials
// a fresh one. Used by the agent-connect control command.
func (b *AgentBridge) ForceReconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		b.agent.Close()
		b.agent = nil
	}
	return b.connectAgentLocked()
}

// HealthMonitor runs in the background pinging the agent. It polls
// quickly while the VM is booting or the user agent is still coming
// up, then falls back to the configured interval. Caller is
// responsible for kicking off the goroutine and supplying the steady
// interval (typically resolved from COVE_AGENT_HEALTH_INTERVAL).
func (b *AgentBridge) HealthMonitor(steady time.Duration) {
	ctx := b.host.LifecycleContext()

	// Wait briefly for the VM to boot before starting health checks.
	select {
	case <-ctx.Done():
		return
	case <-time.After(startupAgentHealthInterval):
	}

	slog.Debug("agent-health: monitor started", "interval", steady)

	failCount := 0
	for {
		if !b.host.Running() {
			return
		}

		b.healthCheckOnce(ctx, &failCount)

		next := b.NextAgentHealthInterval(steady)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next):
		}
	}
}

// NextAgentHealthInterval picks the cadence for the next health
// check: the short startup interval until both daemon and user agent
// are connected, then the supplied steady interval.
func (b *AgentBridge) NextAgentHealthInterval(steady time.Duration) time.Duration {
	b.healthMu.RLock()
	daemon := b.health.DaemonStatus
	user := b.health.UserStatus
	b.healthMu.RUnlock()
	if daemon == "connected" && user == "connected" {
		return steady
	}
	return startupAgentHealthInterval
}

func (b *AgentBridge) healthCheckOnce(ctx context.Context, failCount *int) {
	state, err := b.currentVMState()
	if err != nil || AgentUnavailableForVMState(state) != nil {
		b.SetHealthStatus("disconnected", "", "vm not running")
		return
	}

	b.mu.RLock()
	a := b.agent
	b.mu.RUnlock()

	if a != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		agentVer, err := a.Ping(pingCtx)
		cancel()
		if err == nil {
			*failCount = 0
			b.MarkAgentConnected(agentVer)
			b.checkAgentVersion(agentVer)
			b.healthCheckUserAgent(ctx)
			b.healthCheckGUISession(ctx)
			return
		}
		slog.Warn("agent-health: ping failed",
			"err", err, "attempt", *failCount+1)
	}

	*failCount++
	b.MarkAgentReconnecting(fmt.Sprintf("ping failed (attempt %d)", *failCount))

	b.mu.Lock()
	if b.agent != nil {
		b.agent.Close()
		b.agent = nil
	}
	err = b.connectAgentLocked()
	b.mu.Unlock()

	if err != nil {
		b.SetHealthStatus("disconnected", "", err.Error())
		return
	}

	b.mu.RLock()
	a = b.agent
	b.mu.RUnlock()

	if a != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		agentVer, err := a.Ping(pingCtx)
		cancel()
		if err == nil {
			*failCount = 0
			b.MarkAgentConnected(agentVer)
			b.checkAgentVersion(agentVer)
			b.healthCheckUserAgent(ctx)
			b.healthCheckGUISession(ctx)
			return
		}
		b.SetHealthStatus("disconnected", "", fmt.Sprintf("reconnected but ping failed: %v", err))
	}
}

// MarkAgentConnected transitions the agent into the "connected"
// state. If the previous state recorded a disconnect, this is the
// recovery edge — emit an INFO log with elapsed downtime so
// operators can see how long the agent was unreachable.
func (b *AgentBridge) MarkAgentConnected(version string) {
	now := b.now()
	b.healthMu.Lock()
	wasDisconnected := !b.health.DisconnectAt.IsZero()
	downtime := time.Duration(0)
	if wasDisconnected {
		downtime = now.Sub(b.health.DisconnectAt)
	}
	b.health.DaemonStatus = "connected"
	if version != "" {
		b.health.Version = version
	}
	b.health.LastErr = ""
	b.health.LastPing = now
	b.health.DisconnectAt = time.Time{}
	b.healthMu.Unlock()

	if wasDisconnected {
		slog.Info("agent-health: reconnected",
			"version", version, "downtime", downtime.Round(time.Millisecond))
		return
	}
	slog.Debug("agent-health: ping ok", "version", version)
}

// MarkAgentReconnecting transitions the agent into the "reconnecting"
// state. Captures the first-failure timestamp so the eventual
// recovery edge can report accurate downtime.
func (b *AgentBridge) MarkAgentReconnecting(reason string) {
	now := b.now()
	b.healthMu.Lock()
	if b.health.DisconnectAt.IsZero() {
		b.health.DisconnectAt = now
	}
	b.health.DaemonStatus = "reconnecting"
	b.health.LastErr = reason
	b.healthMu.Unlock()
}

// now returns the host's current time, falling back to time.Now()
// when the bridge has no host (zero-value test bridges).
func (b *AgentBridge) now() time.Time {
	if b.host == nil {
		return time.Now()
	}
	return b.host.Now()
}

// checkAgentVersion compares the guest agent version with the host
// version. On a comparable mismatch where the guest is older, asks
// the host to consider auto-upgrade. Equal/unknown versions are
// no-ops.
func (b *AgentBridge) checkAgentVersion(agentVer string) {
	b.healthMu.RLock()
	checked := b.health.VersionChecked
	b.healthMu.RUnlock()
	if checked {
		return
	}

	b.healthMu.Lock()
	b.health.VersionChecked = true
	b.healthMu.Unlock()

	if b.host == nil {
		return
	}

	b.healthMu.RLock()
	attempted := b.health.UpgradeAttempted
	b.healthMu.RUnlock()
	if attempted {
		return
	}

	resetCheck := func() {
		b.healthMu.Lock()
		b.health.VersionChecked = false
		b.healthMu.Unlock()
	}
	if b.host.MaybeAutoUpgradeAgent(agentVer, resetCheck) {
		b.healthMu.Lock()
		b.health.UpgradeAttempted = true
		b.healthMu.Unlock()
	}
}

func (b *AgentBridge) healthCheckUserAgent(ctx context.Context) {
	ua, err := b.GetUserAgent()
	if err != nil {
		b.healthMu.Lock()
		b.health.UserStatus = "disconnected"
		b.healthMu.Unlock()
		return
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, err = ua.UserExec(pingCtx, []string{"true"}, nil, "")
	cancel()
	b.healthMu.Lock()
	if err == nil {
		b.health.UserStatus = "connected"
	} else {
		b.health.UserStatus = "disconnected"
	}
	b.healthMu.Unlock()
}

func (b *AgentBridge) healthCheckGUISession(ctx context.Context) {
	b.mu.RLock()
	a := b.agent
	b.mu.RUnlock()
	if a == nil || b.host == nil {
		return
	}

	session, ok, err := b.host.ProbeGUISession(ctx, a)
	if err != nil {
		slog.Debug("agent-health: gui session probe failed", "err", err)
		return
	}
	b.healthMu.Lock()
	b.health.GUISession = session
	b.health.GUISessionActive = ok
	b.healthMu.Unlock()
}

// Summary returns a short status string for UI display. Thread-safe.
func (b *AgentBridge) Summary() string {
	b.healthMu.RLock()
	h := b.health
	b.healthMu.RUnlock()

	return AgentHealthSummaryWithNeverConnected(h, b.agentNeverConnectedSummary())
}

// agentNeverConnectedSummary describes the pre-first-connect state.
// It uses the VM's agent config to differentiate "fresh install,
// agent will appear after first boot" from "agent expected, currently
// waiting" and "no agent configured for this VM."
func (b *AgentBridge) agentNeverConnectedSummary() string {
	if b.host == nil {
		return "Agent: not installed"
	}
	cfg, err := vmconfig.Load(b.host.VMDir())
	if err != nil || cfg == nil || cfg.Agent == nil {
		return "Agent: not installed"
	}
	if !cfg.Agent.Requested {
		return "Agent: not installed"
	}
	if !cfg.Agent.Verified {
		return "Agent: starting (first boot)"
	}
	return "Agent: connecting..."
}

// SetHealthStatus is the legacy entry point used by callers that
// bypass MarkAgent* (e.g. healthCheckOnce when the VM is not
// running). It must keep the same disconnect-edge semantics or
// downtime accounting drifts.
func (b *AgentBridge) SetHealthStatus(status, version, lastErr string) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	b.health.DaemonStatus = status
	if version != "" {
		b.health.Version = version
	}
	b.health.LastErr = lastErr
	now := b.now()
	switch status {
	case "connected":
		b.health.LastPing = now
		b.health.DisconnectAt = time.Time{}
	case "disconnected", "reconnecting":
		if b.health.DisconnectAt.IsZero() {
			b.health.DisconnectAt = now
		}
	}
}

// SetLastPingForTest writes the LastPing field directly. Intended for
// package-main runtime-policy tests that need to drive the
// idle-timeout edge from a fixed timestamp.
func (b *AgentBridge) SetLastPingForTest(t time.Time) {
	b.healthMu.Lock()
	b.health.LastPing = t
	b.healthMu.Unlock()
}

// avoid "log imported and not used" if log path is removed in future.
var _ = log.Print
