package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
	"time"
)

//go:embed internal/coved/com.cove.daemon.plist.tmpl
var covedPlistTemplate string

type daemonStatus struct {
	Version                string `json:"version"`
	UptimeS                int64  `json:"uptime_s"`
	VMsManaged             int    `json:"vms_managed"`
	ImageGCLastRunTS       string `json:"image_gc_last_run_ts,omitempty"`
	ImageGCRunsTotal       int64  `json:"image_gc_runs_total"`
	ImageGCBytesFreedTotal int64  `json:"image_gc_bytes_freed_total"`
}

type daemonPaths struct {
	SocketPath string
	PIDPath    string
	PlistPath  string
	LogPath    string
	CovedPath  string
}

var (
	daemonDialTimeout = 2 * time.Second
	daemonRunCommand  = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	daemonExecutable = os.Executable
)

func daemonCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		return fmt.Errorf("usage: cove daemon <status|start|stop>")
	}
	switch args[0] {
	case "status":
		status, err := queryDaemonStatus(defaultDaemonPaths().SocketPath)
		if err != nil {
			return err
		}
		fmt.Printf("version: %s\nuptime_s: %d\nvms_managed: %d\nimage_gc_last_run_ts: %s\nimage_gc_runs_total: %d\nimage_gc_bytes_freed_total: %d\n",
			status.Version, status.UptimeS, status.VMsManaged, status.ImageGCLastRunTS, status.ImageGCRunsTotal, status.ImageGCBytesFreedTotal)
		return nil
	case "start":
		return daemonStartCommand(args[1:])
	case "stop":
		return daemonStopCommand(args[1:])
	default:
		return fmt.Errorf("unknown daemon command: %s", args[0])
	}
}

func daemonStartCommand(args []string) error {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	covedPath := fs.String("coved", "", "path to coved binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove daemon start [-coved <path>]")
	}
	paths := defaultDaemonPaths()
	if *covedPath != "" {
		paths.CovedPath = *covedPath
	}
	if err := installDaemonPlist(paths); err != nil {
		return err
	}
	out, err := daemonRunCommand("launchctl", "load", paths.PlistPath)
	if err != nil {
		return fmt.Errorf("launchctl load: %w: %s", err, bytes.TrimSpace(out))
	}
	fmt.Printf("daemon plist: %s\n", paths.PlistPath)
	return nil
}

func daemonStopCommand(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: cove daemon stop")
	}
	paths := defaultDaemonPaths()
	out, err := daemonRunCommand("launchctl", "unload", paths.PlistPath)
	if err != nil {
		return fmt.Errorf("launchctl unload: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func queryDaemonStatus(socketPath string) (daemonStatus, error) {
	conn, err := net.DialTimeout("unix", socketPath, daemonDialTimeout)
	if err != nil {
		return daemonStatus{}, fmt.Errorf("daemon status: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("STATUS\n")); err != nil {
		return daemonStatus{}, fmt.Errorf("daemon status write: %w", err)
	}
	var status daemonStatus
	if err := json.NewDecoder(conn).Decode(&status); err != nil {
		return daemonStatus{}, fmt.Errorf("daemon status decode: %w", err)
	}
	return status, nil
}

func installDaemonPlist(paths daemonPaths) error {
	if err := os.MkdirAll(filepath.Dir(paths.PlistPath), 0755); err != nil {
		return fmt.Errorf("create launch agents dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.SocketPath), 0700); err != nil {
		return fmt.Errorf("create daemon state dir: %w", err)
	}
	t, err := template.New("coved.plist").Parse(covedPlistTemplate)
	if err != nil {
		return fmt.Errorf("parse plist template: %w", err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, paths); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}
	return os.WriteFile(paths.PlistPath, b.Bytes(), 0644)
}

func defaultDaemonPaths() daemonPaths {
	home, _ := os.UserHomeDir()
	covePath := "coved"
	if exe, err := daemonExecutable(); err == nil && exe != "" {
		covePath = filepath.Join(filepath.Dir(exe), "coved")
	}
	stateDir := filepath.Join(home, ".vz")
	return daemonPaths{
		SocketPath: filepath.Join(stateDir, "cove.sock"),
		PIDPath:    filepath.Join(stateDir, "cove.pid"),
		PlistPath:  filepath.Join(home, "Library", "LaunchAgents", "com.cove.daemon.plist"),
		LogPath:    filepath.Join(stateDir, "coved.log"),
		CovedPath:  covePath,
	}
}
