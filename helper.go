// helper.go — privileged helper daemon for cove.
//
// cove's GUI install path needs to chown files to root:wheel on a mounted VM
// disk image. Calling AuthorizationCreate from a background goroutine can hang
// indefinitely when no foreground NSWindow is available (filed: vz-macos-hdz),
// and even when it works it prompts for credentials on every run.
//
// The helper is a small LaunchDaemon that runs as root and listens on
// /var/run/cove-helper.sock. It accepts typed manifests (apply_manifest op,
// see elevated_exec.go) and dispatches them to the same Go file-op runner
// the AEWP path uses — no shell, no arbitrary script execution.
// Authentication is by peer UID: only the user who installed the helper
// can talk to it. Installation requires one admin auth (via the AEWP path)
// and persists across reboots.
//
// Trust model: the helper is "cove's elevated half." Once installed, it
// trusts the installing user's cove binary to send sensible manifests, but
// the manifest schema strictly bounds what it can do — no exec hatch.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	helperSocketPath = "/var/run/cove-helper.sock"
	helperUIDPath    = "/var/run/cove-helper.uid"
	helperPlistPath  = "/Library/LaunchDaemons/com.tmc.cove.helper.plist"
	helperBinaryPath = "/usr/local/libexec/cove-helper"
	helperLabel      = "com.tmc.cove.helper"
)

// helperRequest is the JSON request format accepted by the helper.
type helperRequest struct {
	Op       string          `json:"op"`
	Manifest json.RawMessage `json:"manifest,omitempty"` // for apply_manifest: encoded elevatedManifest
}

// helperResponse is the JSON response format returned by the helper.
type helperResponse struct {
	OK     bool   `json:"ok"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Error  string `json:"error,omitempty"`
}

// runManifestViaHelper attempts to run a typed elevation manifest via the
// helper. It returns (true, err) if the helper handled the request (err may
// be non-nil if the manifest itself failed), or (false, err) if the helper
// is not available and the caller should fall back to a different path.
func runManifestViaHelper(manifestJSON []byte) (handled bool, err error) {
	if runtime.GOOS != "darwin" {
		return false, nil
	}

	conn, dialErr := dialHelper()
	if dialErr != nil {
		return false, dialErr
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Minute))

	enc := json.NewEncoder(conn)
	if err := enc.Encode(helperRequest{Op: "apply_manifest", Manifest: manifestJSON}); err != nil {
		return true, fmt.Errorf("send request: %w", err)
	}

	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return true, fmt.Errorf("read response: %w", err)
	}
	if resp.Stdout != "" {
		os.Stdout.WriteString(resp.Stdout)
	}
	if resp.Stderr != "" {
		os.Stderr.WriteString(resp.Stderr)
	}
	if !resp.OK {
		return true, fmt.Errorf("helper: %s", resp.Error)
	}
	return true, nil
}

// dialHelper connects to the helper socket. Returns errHelperUnavailable if
// the socket doesn't exist.
func dialHelper() (net.Conn, error) {
	if _, err := os.Stat(helperSocketPath); err != nil {
		if os.IsNotExist(err) {
			return nil, errHelperUnavailable
		}
		return nil, err
	}
	conn, err := net.DialTimeout("unix", helperSocketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial helper: %w", err)
	}
	return conn, nil
}

var errHelperUnavailable = errors.New("cove helper not installed")

// helperInstalled reports whether the helper LaunchDaemon plist and binary are
// in place. It does not verify the daemon is actually running.
func helperInstalled() bool {
	if _, err := os.Stat(helperPlistPath); err != nil {
		return false
	}
	if _, err := os.Stat(helperBinaryPath); err != nil {
		return false
	}
	return true
}

// runHelperCmd dispatches `cove helper <subcommand>`.
func runHelperCmd(args []string) error {
	if len(args) == 0 {
		return helperUsage()
	}
	switch args[0] {
	case "install":
		return helperInstall()
	case "uninstall":
		return helperUninstall()
	case "status":
		return helperStatus()
	case "daemon":
		return helperDaemon()
	case "help", "-h", "--help":
		return helperUsage()
	default:
		return fmt.Errorf("unknown helper subcommand: %s", args[0])
	}
}

func helperUsage() error {
	fmt.Println(`Usage: cove helper <subcommand>

Subcommands:
  install     Install the privileged helper (one-time admin auth required)
  uninstall   Remove the privileged helper
  status      Show whether the helper is installed and running
  daemon      Run as the helper daemon (used by launchd; not for direct use)

The helper eliminates per-run sudo prompts when cove provisions VMs. Once
installed, cove operations that need root (chown root:wheel inside mounted
disk images) are routed through the helper without further authentication
prompts.`)
	return nil
}

// helperInstall installs the helper binary and LaunchDaemon plist with one
// admin auth dialog. The current cove binary is copied to /usr/local/libexec.
func helperInstall() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("helper is darwin-only")
	}

	myPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cove binary: %w", err)
	}
	myPath, err = filepath.EvalSymlinks(myPath)
	if err != nil {
		return fmt.Errorf("resolve cove binary: %w", err)
	}

	uid := os.Getuid()
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>helper</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>/var/log/cove-helper.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/cove-helper.log</string>
</dict>
</plist>
`, helperLabel, helperBinaryPath)

	tmpPlist, err := os.CreateTemp("", "cove-helper-*.plist")
	if err != nil {
		return err
	}
	defer os.Remove(tmpPlist.Name())
	if _, err := tmpPlist.WriteString(plist); err != nil {
		return err
	}
	tmpPlist.Close()

	tmpUID, err := os.CreateTemp("", "cove-helper-uid-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmpUID.Name())
	fmt.Fprintf(tmpUID, "%d\n", uid)
	tmpUID.Close()

	manifest := &elevatedManifest{
		MkdirAll: []string{filepath.Dir(helperBinaryPath)},
		CopyFiles: []elevatedCopy{
			{Src: myPath, Dst: helperBinaryPath, Mode: "0755", Owner: "root:wheel"},
			{Src: tmpPlist.Name(), Dst: helperPlistPath, Mode: "0644", Owner: "root:wheel"},
			{Src: tmpUID.Name(), Dst: helperUIDPath, Mode: "0644", Owner: "root:wheel"},
		},
		LaunchctlBootout:   []string{helperLabel},
		LaunchctlBootstrap: []string{helperPlistPath},
	}

	fmt.Println("Installing cove privileged helper.")
	fmt.Println("You will be prompted once for your admin password. After this, cove")
	fmt.Println("operations that need root (e.g. provisioning a new VM) will not")
	fmt.Println("require further prompts.")
	fmt.Println()

	if err := runElevated(manifest,
		elevationPrompt("Install cove privileged helper (skips future password prompts).")); err != nil {
		return fmt.Errorf("install helper: %w", err)
	}

	// Wait briefly for the daemon to start listening.
	for range 20 {
		if _, err := os.Stat(helperSocketPath); err == nil {
			fmt.Println("Helper installed and running.")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("Helper installed. Daemon may take a moment to start listening.")
	return nil
}

// helperUninstall removes the LaunchDaemon and helper binary. Requires admin.
func helperUninstall() error {
	manifest := &elevatedManifest{
		LaunchctlBootout: []string{helperLabel},
		RemoveFiles: []string{
			helperPlistPath,
			helperBinaryPath,
			helperUIDPath,
			helperSocketPath,
		},
	}
	if err := runElevated(manifest,
		elevationPrompt("Remove cove privileged helper.")); err != nil {
		return fmt.Errorf("uninstall helper: %w", err)
	}
	fmt.Println("Helper uninstalled.")
	return nil
}

// helperStatus prints whether the helper is installed and the socket reachable.
func helperStatus() error {
	fmt.Printf("Plist:   %s\n", helperPlistPath)
	if _, err := os.Stat(helperPlistPath); err == nil {
		fmt.Println("  installed")
	} else {
		fmt.Println("  missing")
	}
	fmt.Printf("Binary:  %s\n", helperBinaryPath)
	if _, err := os.Stat(helperBinaryPath); err == nil {
		fmt.Println("  installed")
	} else {
		fmt.Println("  missing")
	}
	fmt.Printf("Socket:  %s\n", helperSocketPath)
	if _, err := os.Stat(helperSocketPath); err == nil {
		fmt.Println("  present")
	} else {
		fmt.Println("  missing")
	}
	if data, err := os.ReadFile(helperUIDPath); err == nil {
		fmt.Printf("Owner UID: %s", string(data))
	}

	conn, err := dialHelper()
	if err != nil {
		fmt.Printf("Connect:  failed (%v)\n", err)
		return nil
	}
	conn.Close()
	fmt.Println("Connect:  ok")
	return nil
}

// helperDaemon runs the helper event loop. Invoked by launchd via
// `cove helper daemon`. Must be run as root.
func helperDaemon() error {
	if os.Getuid() != 0 {
		return fmt.Errorf("helper daemon must run as root (got uid %d)", os.Getuid())
	}

	allowedUID, err := readHelperUID()
	if err != nil {
		return fmt.Errorf("read helper uid: %w", err)
	}

	// Recreate the socket on every start.
	os.Remove(helperSocketPath)
	l, err := net.Listen("unix", helperSocketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer l.Close()

	// Permissive enough for the owning user; peer-uid check enforces the real
	// access policy.
	if err := os.Chmod(helperSocketPath, 0666); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	fmt.Fprintf(os.Stderr, "cove-helper: listening on %s, allowed uid=%d\n",
		helperSocketPath, allowedUID)

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cove-helper: accept error: %v\n", err)
			continue
		}
		go handleHelperConn(conn, allowedUID)
	}
}

func readHelperUID() (int, error) {
	data, err := os.ReadFile(helperUIDPath)
	if err != nil {
		return 0, err
	}
	uid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse uid: %w", err)
	}
	return uid, nil
}

func handleHelperConn(conn net.Conn, allowedUID int) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Minute))

	uc, ok := conn.(*net.UnixConn)
	if !ok {
		writeHelperError(conn, "expected unix conn")
		return
	}
	peerUID, err := unixPeerUID(uc)
	if err != nil {
		writeHelperError(conn, fmt.Sprintf("peer uid: %v", err))
		return
	}
	if peerUID != allowedUID {
		writeHelperError(conn, fmt.Sprintf("peer uid %d not authorized (allowed: %d)",
			peerUID, allowedUID))
		return
	}

	var req helperRequest
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		writeHelperError(conn, fmt.Sprintf("decode request: %v", err))
		return
	}

	switch req.Op {
	case "apply_manifest":
		runHelperManifest(conn, req.Manifest)
	case "ping":
		json.NewEncoder(conn).Encode(helperResponse{OK: true})
	default:
		writeHelperError(conn, fmt.Sprintf("unknown op: %s", req.Op))
	}
}

func runHelperManifest(conn net.Conn, manifestJSON []byte) {
	if len(manifestJSON) == 0 {
		writeHelperError(conn, "empty manifest")
		return
	}
	var m elevatedManifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		writeHelperError(conn, fmt.Sprintf("parse manifest: %v", err))
		return
	}
	if err := runElevatedManifest(&m); err != nil {
		writeHelperError(conn, err.Error())
		return
	}
	json.NewEncoder(conn).Encode(helperResponse{OK: true})
}

func writeHelperError(w io.Writer, msg string) {
	json.NewEncoder(w).Encode(helperResponse{OK: false, Error: msg})
}

// unixPeerUID returns the effective UID of the peer of a unix-domain socket
// connection. On darwin this uses LOCAL_PEERCRED via GetsockoptXucred.
func unixPeerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid int
	var sErr error
	cErr := raw.Control(func(fd uintptr) {
		var xu *unix.Xucred
		xu, sErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if sErr == nil && xu != nil {
			uid = int(xu.Uid)
		}
	})
	if cErr != nil {
		return 0, cErr
	}
	return uid, sErr
}
