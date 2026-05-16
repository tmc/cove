package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	LifecycleEnforced      uint64 `json:"lifecycle_enforced"`
	LifecycleLastRunTS     string `json:"lifecycle_last_run_ts,omitempty"`
}

type daemonPaths struct {
	SocketPath string
	PIDPath    string
	PlistPath  string
	LogPath    string
	CovedPath  string
}

type daemonErrorOutput struct {
	Command string `json:"command"`
	Error   string `json:"error"`
	Hint    string `json:"hint,omitempty"`
}

var (
	daemonDialTimeout = 2 * time.Second
	daemonRunCommand  = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	daemonExecutable = os.Executable
)

func daemonCommand(args []string) error {
	if len(args) == 0 {
		printDaemonUsage(os.Stderr)
		return fmt.Errorf("daemon: command required")
	}
	if isHelpArg(args[0]) {
		printDaemonUsage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "status":
		jsonOut, err := parseDaemonStatusArgs(args[1:])
		if err != nil {
			return err
		}
		if len(args) > 1 && isHelpArg(args[1]) {
			fmt.Fprintln(os.Stdout, "Usage: cove daemon status [--json]")
			return nil
		}
		status, err := queryDaemonStatus(defaultDaemonPaths().SocketPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				err = fmt.Errorf("daemon: stopped; start with: cove daemon start")
				if jsonOut {
					if jsonErr := writeDaemonErrorJSON(os.Stdout, "daemon status", err); jsonErr != nil {
						return jsonErr
					}
				}
				return err
			}
			if jsonOut {
				if jsonErr := writeDaemonErrorJSON(os.Stdout, "daemon status", err); jsonErr != nil {
					return jsonErr
				}
			}
			return err
		}
		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(status)
		}
		fmt.Printf("version: %s\nuptime_s: %d\nvms_managed: %d\nimage_gc_last_run_ts: %s\nimage_gc_runs_total: %d\nimage_gc_bytes_freed_total: %d\nlifecycle_enforced: %d\n",
			status.Version, status.UptimeS, status.VMsManaged, status.ImageGCLastRunTS, status.ImageGCRunsTotal, status.ImageGCBytesFreedTotal, status.LifecycleEnforced)
		if status.LifecycleLastRunTS != "" {
			fmt.Printf("lifecycle_last_run_ts: %s\n", status.LifecycleLastRunTS)
		}
		return nil
	case "metrics":
		return daemonMetricsCommand(args[1:])
	case "ui":
		return daemonUICommand(args[1:])
	case "start":
		return daemonStartCommand(args[1:])
	case "stop":
		return daemonStopCommand(args[1:])
	default:
		return fmt.Errorf("unknown daemon command: %s", args[0])
	}
}

func parseDaemonStatusArgs(args []string) (bool, error) {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json", "-json":
			jsonOut = true
		default:
			if isHelpArg(arg) {
				return jsonOut, nil
			}
			return false, fmt.Errorf("usage: cove daemon status [--json]")
		}
	}
	return jsonOut, nil
}

func printDaemonUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove daemon <command>

Manage the background cove coordinator.

Commands:
  status     Show daemon status
  start      Install and load the launch agent
  stop       Unload the launch agent
  metrics    Print Prometheus metrics
  ui         Open the daemon web UI`)
}

func daemonUICommand(args []string) error {
	fs := flag.NewFlagSet("daemon ui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", "127.0.0.1:9877", "web UI address")
	openCmd := fs.String("open", "open", "open command")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove daemon ui [-addr host:port]")
	}
	url := "http://" + *addr
	out, err := daemonRunCommand(*openCmd, url)
	if err != nil {
		return fmt.Errorf("open daemon ui: %w: %s", err, bytes.TrimSpace(out))
	}
	fmt.Println(url)
	return nil
}

func daemonMetricsCommand(args []string) error {
	fs := flag.NewFlagSet("daemon metrics", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	raw := fs.Bool("json", false, "print raw prometheus exposition")
	addr := fs.String("addr", "127.0.0.1:9876", "metrics address")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove daemon metrics [--json] [-addr host:port]")
	}
	body, err := fetchDaemonMetrics("http://" + *addr + "/metrics")
	if err != nil {
		err = daemonMetricsErrorWithHint(err)
		if *raw {
			if jsonErr := writeDaemonErrorJSON(os.Stdout, "daemon metrics", err); jsonErr != nil {
				return jsonErr
			}
		}
		return err
	}
	if *raw {
		fmt.Print(body)
		return nil
	}
	printDaemonMetrics(os.Stdout, body)
	return nil
}

func writeDaemonErrorJSON(w io.Writer, command string, err error) error {
	out := daemonErrorOutput{
		Command: command,
		Error:   err.Error(),
	}
	if strings.Contains(err.Error(), "start with: cove daemon start") {
		out.Hint = "cove daemon start"
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func daemonMetricsErrorWithHint(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "connection refused") {
		return fmt.Errorf("%w; start with: cove daemon start", err)
	}
	return err
}

func fetchDaemonMetrics(url string) (string, error) {
	client := &http.Client{Timeout: daemonDialTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("daemon metrics: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("daemon metrics read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("daemon metrics: %s: %s", resp.Status, bytes.TrimSpace(data))
	}
	return string(data), nil
}

func printDaemonMetrics(w io.Writer, body string) {
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		fmt.Fprintf(w, "%s: %s\n", name, value)
	}
}

func daemonStartCommand(args []string) error {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	covedPath := fs.String("coved", "", "path to coved binary")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
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
	if len(args) == 1 && isHelpArg(args[0]) {
		fmt.Fprintln(os.Stdout, "Usage: cove daemon stop")
		return nil
	}
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
	if _, err := os.Stat(socketPath); err != nil {
		return daemonStatus{}, fmt.Errorf("daemon status: %w", err)
	}
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
