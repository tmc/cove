package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tmc/vz-macos/internal/control/operations"
)

type ServeConfig struct {
	HTTPAddr  string
	ListenURL string
	TokenFile string
	VMList    string
	PerVMAuth bool
	MCPMode   bool
}

func parseServeConfig(args []string) (ServeConfig, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Usage = printServeUsage

	cfg := ServeConfig{HTTPAddr: "127.0.0.1:7777"}
	fs.StringVar(&cfg.HTTPAddr, "http", cfg.HTTPAddr, "HTTP listen address (host:port or :port)")
	fs.StringVar(&cfg.ListenURL, "listen", "", "listen URL: tcp://host:port or unix:///path (overrides -http)")
	fs.StringVar(&cfg.TokenFile, "token-file", "", "master token file path; empty = keychain default")
	fs.BoolVar(&cfg.PerVMAuth, "per-vm-auth", false, "strict mode: require per-VM token for each VM route")
	fs.StringVar(&cfg.VMList, "vms", "", "comma-separated VM name allowlist (empty = all running VMs)")
	fs.BoolVar(&cfg.MCPMode, "mcp", false, "stdio MCP transport: JSON-RPC 2.0 over stdin/stdout")

	if err := fs.Parse(args); err != nil {
		return ServeConfig{}, err
	}
	return cfg, nil
}

func (cfg ServeConfig) Allowlist() []string {
	if cfg.VMList == "" {
		return nil
	}
	var allowlist []string
	for _, name := range strings.Split(cfg.VMList, ",") {
		if t := strings.TrimSpace(name); t != "" {
			allowlist = append(allowlist, t)
		}
	}
	return allowlist
}

func (cfg ServeConfig) ListenAddr() (string, error) {
	if cfg.ListenURL == "" {
		return cfg.HTTPAddr, nil
	}
	return parseListenURL(cfg.ListenURL)
}

func runServeCmd(args []string) error {
	cfg, err := parseServeConfig(args)
	if err != nil {
		return err
	}

	if cfg.MCPMode {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}
		vmDir := filepath.Join(home, ".vz", "vms")
		fmt.Fprintln(os.Stderr, "cove serve: MCP stdio transport; reading JSON-RPC on stdin")
		return runMCPStdio(vmDir)
	}

	masterToken, err := LoadOrCreateMasterToken(cfg.TokenFile)
	if err != nil {
		return fmt.Errorf("load master token: %w", err)
	}

	checkSharedHost(cfg.PerVMAuth, cfg.TokenFile, nil)
	dhcpListenAddr := cfg.HTTPAddr
	if cfg.ListenURL != "" {
		dhcpListenAddr = cfg.ListenURL
	}
	warnIfDHCPLeaseTimeLongForServe(os.Stderr, dhcpListenAddr)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	opsDir := filepath.Join(home, ".vz", "operations")
	store, err := operations.NewFileOperationStore(opsDir)
	if err != nil {
		return fmt.Errorf("operations store: %w", err)
	}
	registry, err := operations.NewOperationRegistry(store)
	if err != nil {
		return fmt.Errorf("operations registry: %w", err)
	}

	vmDir := filepath.Join(home, ".vz", "vms")
	gw, err := NewGateway(vmDir, masterToken, cfg.PerVMAuth, cfg.Allowlist(), registry)
	if err != nil {
		return fmt.Errorf("create gateway: %w", err)
	}

	addr, err := cfg.ListenAddr()
	if err != nil {
		return fmt.Errorf("parse -listen: %w", err)
	}

	ln, err := gw.Start(addr)
	if err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}
	fmt.Fprintf(os.Stderr, "cove serve: listening at http://%s\n", ln.Addr())
	fmt.Fprintf(os.Stderr, "cove serve: token file: %s\n", resolveTokenFilePath(cfg.TokenFile))

	// Run HTTP server in background; block on signal.
	errCh := make(chan error, 1)
	go func() {
		errCh <- http.Serve(ln, gw)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "\ncove serve: received %s, shutting down\n", sig)
		gw.Stop()
		ln.Close()
		return nil
	case err := <-errCh:
		if err != nil && !isClosedError(err) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}

func parseListenURL(u string) (string, error) {
	switch {
	case strings.HasPrefix(u, "tcp://"):
		return strings.TrimPrefix(u, "tcp://"), nil
	case strings.HasPrefix(u, "unix://"):
		return strings.TrimPrefix(u, "unix://"), nil
	default:
		return "", fmt.Errorf("unsupported scheme in %q (use tcp:// or unix://)", u)
	}
}

func resolveTokenFilePath(tokenFile string) string {
	if tokenFile != "" {
		return tokenFile
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "gateway.token") + " (or keychain)"
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection")
}

// checkSharedHost warns when multiple users are logged in and master-token
// mode is active. The userLister func is injectable for tests; pass nil to
// use the real /usr/bin/who.
func checkSharedHost(perVMAuth bool, tokenFile string, userLister func() ([]string, error)) {
	if perVMAuth || tokenFile != "" {
		return
	}
	if userLister == nil {
		userLister = whoUsers
	}
	users, err := userLister()
	if err != nil {
		return // fail-open: hint, not a security boundary
	}
	seen := make(map[string]bool)
	for _, u := range users {
		if u != "" {
			seen[u] = true
		}
	}
	if len(seen) > 1 {
		fmt.Fprintf(os.Stderr,
			"cove serve: detected %d distinct logged-in users; master-token mode grants full agent_exec inside every VM. For shared hosts, pass -per-vm-auth to require per-VM tokens. See docs/reference/http-api.md#multi-user-hosts.\n",
			len(seen))
	}
}

// whoUsers parses /usr/bin/who output and returns the distinct usernames.
func whoUsers() ([]string, error) {
	out, err := exec.Command("/usr/bin/who").Output()
	if err != nil {
		return nil, err
	}
	var users []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			users = append(users, fields[0])
		}
	}
	return users, nil
}

func printServeUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cove serve [options]

Start a multi-VM HTTP gateway. GET /v1/vms lists known/allowed VMs; per-VM routes proxy only running VMs with reachable control sockets.

Options:
  -http <addr>         HTTP listen address (default: 127.0.0.1:7777)
  -listen <url>        Listen URL: tcp://host:port or unix:///path (overrides -http)
  -token-file <path>   Master token file path; empty = macOS keychain default
  -per-vm-auth         Strict mode: require per-VM control.token for each VM route
  -vms <list>          Comma-separated VM allowlist (default: all known VMs)
  -mcp                 stdio MCP transport: JSON-RPC 2.0 over stdin/stdout

Auth:
  Default: master token in macOS keychain (service=cove-gateway).
  On first start, a 64-hex-char token is generated and stored.
  Use -token-file for CI/headless environments.

  Every authenticated request requires:
    Authorization: Bearer <master-token>

  In -per-vm-auth mode, /v1/vms/<name>/* requires each VM's own
  control.token instead.

Routes:
  GET  /healthz                     no auth required
  GET  /v1/vms                      list known/allowed VMs
  POST /v1/vms                      create VM (async, returns 202 + operation)
  /v1/vms/<name>/*                  proxy to running VM's control socket
  GET  /v1/operations/<id>          poll operation status
  GET  /v1/operations/<id>/events   SSE stream of operation progress
  GET  /v1/operations               list recent operations

VM discovery:
  Lists configured VMs from ~/.vz/vms. Polls reachable control.sock files every 2s and hot-adds/removes proxy routes.
  Pass -vms to restrict to a specific set of VMs.

Examples:
  cove serve                               # localhost:7777, keychain token
  cove serve -http :7778                   # custom port
  cove serve -token-file ~/.cove/api.tok   # file token for CI
  cove serve -per-vm-auth                  # per-VM strict mode`)
}
