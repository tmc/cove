// agent_routing.go - Single source of truth for daemon vs user-agent routing.
//
// macOS guests run two agents inside the VM:
//
//   - Daemon agent (vsock port 1024): root context, system ops.
//   - User agent (vsock port 1025): logged-in user session, inherits TCC/FDA.
//
// Most agent operations have a clear home. System ops (launchctl on system
// services, /var paths, OS-level provisioning) belong on the daemon. TCC-
// protected ops (VirtioFS user mounts, ~/Library writes, AppleScript, screen
// recording) must run on the user agent — the daemon has no FDA grant on a
// SIP-enabled guest, and the only zero-touch path to one (PPPC profile signed
// by an MDM cert) is closed on macOS 11+. See docs/research/tcc-via-user-agent.md.
//
// agentRouteFor reads the op type and (when relevant) the path, and returns the
// route. Callers that produce or read TCC-protected paths should use this
// helper rather than calling getAgent / getUserAgent directly. Linux guests
// always get routeDaemon — there is no UserAgent service.

package main

import "strings"

// agentRoute selects which guest agent should service an op.
type agentRoute int

const (
	routeDaemon agentRoute = iota // vsock 1024, root, no TCC/FDA
	routeUser                     // vsock 1025, user session, TCC/FDA inherited
)

func (r agentRoute) String() string {
	switch r {
	case routeUser:
		return "user"
	default:
		return "daemon"
	}
}

// agentRouteFor returns the appropriate route for op. path may be empty for
// ops that do not address a guest path (exec, mount, etc.). For Linux guests,
// the user agent is unavailable and routeDaemon is always returned.
//
// The "auto" suffix on op selects the path-aware route — system paths stay on
// the daemon, user paths go to the user agent. Explicit ops keep their home:
//   - "agent-exec" stays on the daemon (caller asked for root).
//   - "agent-user-exec" / "agent-user-exec-stream" stay on the user agent.
//
// op is the control-socket request type (without the agent- prefix). path is
// the guest path the op will touch, if any.
func agentRouteFor(op, path string, linuxGuest bool) agentRoute {
	if linuxGuest {
		return routeDaemon
	}
	switch op {
	case "user-exec", "user-exec-stream", "user-exec-auto":
		return routeUser
	case "mount-volumes":
		// mount_virtiofs itself requires root and stays on the daemon. The
		// TCC restriction only bites file operations *through* the mount;
		// callers that read or write inside /Volumes/<tag> use the path-aware
		// route below.
		return routeDaemon
	case "exec", "exec-stream", "ping", "info", "shutdown", "reboot",
		"sshd", "connect", "status":
		return routeDaemon
	case "read", "write", "cp":
		if isUserPath(path) {
			return routeUser
		}
		return routeDaemon
	}
	return routeDaemon
}

// isUserPath reports whether p is a guest path that requires user-context
// (TCC/FDA) to access. The daemon agent runs as root and can read most of
// /Users — but TCC adds a second layer that blocks even root on:
//
//   - /Users/<name>/Library, ~/Documents, ~/Desktop, ~/Downloads, ~/Movies,
//     ~/Music, ~/Pictures (Files and Folders / Full Disk Access)
//   - /Volumes/<tag> for any non-system volume (VirtioFS, USB, removable)
//
// /Volumes/Macintosh HD is the system root and stays on the daemon. Plain
// /Users/<name> and ~/Public are not TCC-protected, so they stay on the
// daemon by default — pass the protected subdirectory explicitly to opt in.
//
// /tmp, /var, /Library (system Library), /System, /private and /usr always
// stay on the daemon.
func isUserPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/Volumes/") {
		rest := strings.TrimPrefix(p, "/Volumes/")
		if strings.HasPrefix(rest, "Macintosh HD") {
			return false
		}
		return true
	}
	if !strings.HasPrefix(p, "/Users/") {
		return false
	}
	// Strip /Users/<name>/ to inspect the user-relative tail.
	rest := strings.TrimPrefix(p, "/Users/")
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return false
	}
	tail := rest[slash+1:]
	for _, dir := range tccProtectedHomeDirs {
		if tail == dir || strings.HasPrefix(tail, dir+"/") {
			return true
		}
	}
	return false
}

// tccProtectedHomeDirs lists the user-relative subdirectories that TCC's
// Files and Folders / Full Disk Access policy gates even for root.
var tccProtectedHomeDirs = []string{
	"Library",
	"Documents",
	"Desktop",
	"Downloads",
	"Movies",
	"Music",
	"Pictures",
}
