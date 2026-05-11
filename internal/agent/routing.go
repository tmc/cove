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
// RouteFor reads the op type and (when relevant) the path, and returns the
// route. Callers that produce or read TCC-protected paths should use this
// helper rather than calling getAgent / getUserAgent directly. Linux guests
// always get RouteDaemon — there is no UserAgent service.

package agent

import (
	"path/filepath"
	"strings"
)

// Route selects which guest agent should service an op.
type Route int

const (
	RouteDaemon Route = iota // vsock 1024, root, no TCC/FDA
	RouteUser                // vsock 1025, user session, TCC/FDA inherited
)

func (r Route) String() string {
	switch r {
	case RouteUser:
		return "user"
	default:
		return "daemon"
	}
}

// Port returns the vsock port for r.
func (r Route) Port() uint32 {
	switch r {
	case RouteUser:
		return UserPort
	default:
		return DaemonPort
	}
}

// RouteFor returns the appropriate route for op. path may be empty for
// ops that do not address a guest path (exec, mount, etc.). For Linux guests,
// the user agent is unavailable and RouteDaemon is always returned.
//
// The "auto" suffix on op selects the path-aware route — system paths stay on
// the daemon, user paths go to the user agent. Explicit ops keep their home:
//   - "agent-exec" stays on the daemon (caller asked for root).
//   - "agent-user-exec" / "agent-user-exec-stream" stay on the user agent.
//
// op is the control-socket request type (without the agent- prefix). path is
// the guest path the op will touch, if any.
func RouteFor(op, path string, linuxGuest bool) Route {
	if linuxGuest {
		return RouteDaemon
	}
	switch op {
	case "user-exec", "user-exec-stream", "user-exec-auto":
		return RouteUser
	case "mount-volumes":
		// mount_virtiofs itself requires root and stays on the daemon. The
		// TCC restriction only bites file operations *through* the mount;
		// callers that read or write inside /Volumes/<tag> use the path-aware
		// route below.
		return RouteDaemon
	case "exec", "exec-stream":
		if IsUserPath(path) {
			return RouteUser
		}
		return RouteDaemon
	case "ping", "info", "shutdown", "reboot", "sshd", "connect", "status":
		return RouteDaemon
	case "read", "write", "cp":
		if IsUserPath(path) {
			return RouteUser
		}
		return RouteDaemon
	}
	return RouteDaemon
}

// RouteForExec returns the route for an exec argv. Commands with no
// recognizable path operand stay on the daemon.
func RouteForExec(argv []string, linuxGuest bool) Route {
	path := ExtractPathOperand(argv)
	if path == "" {
		return RouteDaemon
	}
	return RouteFor("exec", path, linuxGuest)
}

// ExtractPathOperand returns the first useful path operand for common file
// commands. It is deliberately conservative: it does not parse shells, expand
// globs, or treat plain words as paths.
func ExtractPathOperand(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	cmd := filepath.Base(argv[0])
	args := argv[1:]

	switch cmd {
	case "ls", "stat", "cat", "head", "tail", "file", "du", "df",
		"mkdir", "rmdir", "rm", "chmod", "chown", "chgrp":
		return lastPathOperand(args)
	case "cp", "mv", "ln":
		return lastPathOperand(args)
	case "test", "[":
		return testPathOperand(args)
	case "tar":
		return tarPathOperand(args)
	}
	return ""
}

func lastPathOperand(args []string) string {
	var last string
	for _, arg := range positionalOperands(args) {
		if looksPathLike(arg) {
			last = arg
		}
	}
	return last
}

func testPathOperand(args []string) string {
	pos := positionalOperands(args)
	if len(pos) == 2 && isUnaryFileTest(pos[0]) && looksPathLike(pos[1]) {
		return pos[1]
	}
	var last string
	for _, arg := range pos {
		if looksPathLike(arg) {
			last = arg
		}
	}
	return last
}

func positionalOperands(args []string) []string {
	var out []string
	parsingFlags := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if parsingFlags && arg == "--" {
			parsingFlags = false
			continue
		}
		if parsingFlags && strings.HasPrefix(arg, "-") && arg != "-" {
			if flagConsumesNext(arg) && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
			}
			continue
		}
		if arg != "-" {
			out = append(out, arg)
		}
	}
	return out
}

func flagConsumesNext(flag string) bool {
	switch flag {
	case "-C", "--directory", "-f", "--file", "-o", "--output",
		"--target-directory", "-t", "--reference", "--owner", "--group",
		"--exclude", "--exclude-from", "--transform":
		return true
	}
	return false
}

func isUnaryFileTest(op string) bool {
	switch op {
	case "-e", "-f", "-d", "-r", "-w", "-x", "-L", "-S", "-b", "-c", "-p":
		return true
	}
	return false
}

func tarPathOperand(args []string) string {
	var file string
	var firstPos string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for _, rest := range args[i+1:] {
				if looksPathLike(rest) {
					return rest
				}
			}
			return ""
		}
		if arg == "-C" || arg == "--directory" {
			if i+1 < len(args) && looksPathLike(args[i+1]) {
				return args[i+1]
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "-C") && len(arg) > 2 {
			path := strings.TrimPrefix(arg, "-C")
			if looksPathLike(path) {
				return path
			}
			continue
		}
		if arg == "-f" || arg == "--file" {
			if i+1 < len(args) && looksPathLike(args[i+1]) {
				file = args[i+1]
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "--file=") {
			path := strings.TrimPrefix(arg, "--file=")
			if looksPathLike(path) {
				file = path
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && strings.Contains(arg, "f") && len(arg) > 2 {
			if i+1 < len(args) && looksPathLike(args[i+1]) {
				file = args[i+1]
				i++
			}
			continue
		}
		if !strings.HasPrefix(arg, "-") && firstPos == "" && looksPathLike(arg) {
			firstPos = arg
		}
	}
	if file != "" {
		return file
	}
	return firstPos
}

func looksPathLike(s string) bool {
	return strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "~/") ||
		strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") ||
		strings.Contains(s, "/")
}

// IsUserPath reports whether p is a guest path that requires user-context
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
func IsUserPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/Volumes/") {
		rest := strings.TrimPrefix(p, "/Volumes/")
		return !strings.HasPrefix(rest, "Macintosh HD")
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
