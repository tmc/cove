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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// helperLaunchdPlist returns the LaunchDaemon plist body for the helper.
//
// KeepAlive is conditional on SuccessfulExit=false, so a daemon that exits 0
// is not respawned — only crashes are. ThrottleInterval=30 caps respawn rate
// so a stale or broken binary cannot churn the icon at ~6/min the way the
// unconditional KeepAlive=true plist did before v0.1.1.
//
// EnvironmentVariables sets HOME=/var/root because launchd does not propagate
// HOME to system daemons. Without it, os.UserHomeDir() returns "" and any
// code path that resolves ~/.vz ends up doing mkdir .vz against cwd / (EROFS),
// which crashes the daemon and triggers a respawn loop.
//
// PATH excludes /usr/local/bin: the helper runs as root and invokes launchctl,
// diskutil, and mount by bare name (see runElevatedManifest). /usr/local/bin
// is admin-writable, so a malicious local-admin process could plant a shim
// there and hijack a root-priv exec. The helper invokes no homebrew binaries.
func helperLaunchdPlist(label, binaryPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
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
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ThrottleInterval</key>
  <integer>30</integer>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>/var/root</string>
    <key>PATH</key>
    <string>/usr/sbin:/sbin:/usr/bin:/bin</string>
  </dict>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>/var/log/cove-helper.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/cove-helper.log</string>
</dict>
</plist>
`, label, binaryPath)
}

// fileSHA256 returns the lowercase hex SHA256 of the file at path. Returns
// an error if the file cannot be read.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// helperBinaryFreshness compares the SHA256 of the installed helper binary
// against the SHA256 of the running cove binary. It returns whether the two
// are identical, a short summary suitable for "cove helper status", and any
// error encountered while reading either binary.
//
// "stale" is the common case during upgrades: the user replaced /usr/local/bin/cove
// with a newer build but didn't re-run `sudo cove helper install`, so the
// LaunchDaemon at /usr/local/libexec/cove-helper still runs the old version.
// A stale binary that crash-loops can hammer launchd; warning loudly here is
// the cheapest path to a clear remediation prompt.
func helperBinaryFreshness() (matches bool, summary string, err error) {
	myPath, err := os.Executable()
	if err != nil {
		return false, "", fmt.Errorf("locate running binary: %w", err)
	}
	myPath, _ = filepath.EvalSymlinks(myPath)

	mySum, err := fileSHA256(myPath)
	if err != nil {
		return false, "", fmt.Errorf("hash running binary: %w", err)
	}
	installedSum, err := fileSHA256(helperBinaryPath)
	if err != nil {
		return false, "", fmt.Errorf("hash installed helper: %w", err)
	}
	if mySum == installedSum {
		return true, fmt.Sprintf("up to date (sha256:%s)", mySum[:12]), nil
	}
	return false, fmt.Sprintf(
		"stale: installed sha256:%s, current sha256:%s\n"+
			"  re-run `sudo cove helper install` to refresh",
		installedSum[:12], mySum[:12]), nil
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

	uid, err := helperInstallOwnerUID(os.Getuid(), os.LookupEnv)
	if err != nil {
		return err
	}
	plist := helperLaunchdPlist(helperLabel, helperBinaryPath)

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
		if _, summary, err := helperBinaryFreshness(); err == nil {
			fmt.Printf("  %s\n", summary)
		}
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
// initHelperLogger installs a daemon-scoped slog default tagged with
// component=cove-helper. Defaults to TextHandler on stderr (which the
// LaunchDaemon plist redirects to /var/log/cove-helper.log); set
// COVE_HELPER_LOG_JSON=1 for JSONHandler output.
func initHelperLogger() *slog.Logger {
	var h slog.Handler
	if os.Getenv("COVE_HELPER_LOG_JSON") == "1" {
		h = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		h = slog.NewTextHandler(os.Stderr, nil)
	}
	logger := slog.New(h).With(slog.String("component", "cove-helper"))
	slog.SetDefault(logger)
	return logger
}

func helperDaemon() error {
	logger := initHelperLogger()

	if os.Getuid() != 0 {
		return fmt.Errorf("helper daemon must run as root (got uid %d)", os.Getuid())
	}

	// launchd does not propagate HOME to system daemons. The plist sets
	// HOME=/var/root, but if a stale plist from before that fix is still
	// installed, exit cleanly so the SuccessfulExit=false KeepAlive does
	// not respawn us in a tight loop. The user must reinstall the helper.
	if home := os.Getenv("HOME"); home == "" {
		logger.Error("HOME unset; refusing to start. Run: sudo cove helper install")
		os.Exit(0)
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

	logger.Info("listening",
		slog.String("socket", helperSocketPath),
		slog.Int("allowedUid", allowedUID),
	)

	for {
		conn, err := l.Accept()
		if err != nil {
			logger.Error("accept", slog.Any("err", err))
			continue
		}
		go handleHelperConn(logger, conn, allowedUID)
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

func helperInstallOwnerUID(uid int, lookup func(string) (string, bool)) (int, error) {
	if uid != 0 {
		return uid, nil
	}
	sudoUID, ok := lookup("SUDO_UID")
	if !ok || strings.TrimSpace(sudoUID) == "" {
		return 0, fmt.Errorf("helper install needs a non-root owner uid; run cove helper install as your user or via sudo")
	}
	ownerUID, err := strconv.Atoi(strings.TrimSpace(sudoUID))
	if err != nil {
		return 0, fmt.Errorf("parse SUDO_UID: %w", err)
	}
	if ownerUID <= 0 {
		return 0, fmt.Errorf("helper install needs a non-root owner uid; got SUDO_UID=%d", ownerUID)
	}
	return ownerUID, nil
}

func handleHelperConn(parent *slog.Logger, conn net.Conn, allowedUID int) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Minute))

	// Request-scoped logger; gains peer-uid and op as soon as we know them.
	log := parent

	uc, ok := conn.(*net.UnixConn)
	if !ok {
		writeHelperError(log, conn, "expected unix conn")
		return
	}
	peerUID, err := unixPeerUID(uc)
	if err != nil {
		writeHelperError(log, conn, fmt.Sprintf("peer uid: %v", err))
		return
	}
	log = log.With(slog.Int("peerUid", peerUID))
	if peerUID != allowedUID {
		writeHelperError(log, conn,
			fmt.Sprintf("peer uid %d not authorized (allowed: %d)", peerUID, allowedUID))
		return
	}

	var req helperRequest
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		writeHelperError(log, conn, fmt.Sprintf("decode request: %v", err))
		return
	}
	log = log.With(slog.String("op", req.Op))

	switch req.Op {
	case "apply_manifest":
		runHelperManifest(log, conn, req.Manifest)
	case "ping":
		log.Info("ping")
		json.NewEncoder(conn).Encode(helperResponse{OK: true})
	default:
		writeHelperError(log, conn, fmt.Sprintf("unknown op: %s", req.Op))
	}
}

func runHelperManifest(log *slog.Logger, conn net.Conn, manifestJSON []byte) {
	log = log.With(slog.Int("manifestBytes", len(manifestJSON)))
	if len(manifestJSON) == 0 {
		writeHelperError(log, conn, "empty manifest")
		return
	}
	var m elevatedManifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		writeHelperError(log, conn, fmt.Sprintf("parse manifest: %v", err))
		return
	}
	if err := runElevatedManifest(&m); err != nil {
		writeHelperError(log, conn, err.Error())
		return
	}
	log.Info("manifest applied")
	json.NewEncoder(conn).Encode(helperResponse{OK: true})
}

// writeHelperError sends an error response to the peer and logs the same
// message at warn level so /var/log/cove-helper.log captures rejection
// reasons (peer-uid mismatch, decode failures, manifest errors). log is
// expected to carry per-request context (peerUid, op) by the time we get
// here, so the message itself stays terse.
func writeHelperError(log *slog.Logger, w io.Writer, msg string) {
	if log != nil {
		log.Warn("reject", slog.String("err", msg))
	}
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
