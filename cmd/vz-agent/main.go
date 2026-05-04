// vz-agent is a guest agent daemon for VMs managed by cove.
// It runs inside the guest (macOS or Linux) and exposes a connect-go API over vsock.
//
// Two modes:
//   - daemon (default, port 1024): runs as root LaunchDaemon, system ops
//   - agent (port 1025): runs as logged-in user LaunchAgent, inherits TCC/FDA
//
// Platform-specific listeners and system info live in OS-specific files.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tmc/vz-macos/proto/agentpbconnect"
)

// relaySpecs collects -relay flags (vsockPort:tcpAddr).
type relaySpecs []string

func (r *relaySpecs) String() string { return strings.Join(*r, ", ") }
func (r *relaySpecs) Set(v string) error {
	*r = append(*r, v)
	return nil
}

const (
	daemonPort = 1024
	agentPort  = 1025
)

func main() {
	mode := flag.String("mode", "", "run mode: daemon (root, port 1024) or agent (user, port 1025)")
	port := flag.Int("port", 0, "vsock port to listen on (overrides mode default)")
	tcpListen := flag.String("tcp-listen", os.Getenv("VZ_AGENT_TCP_LISTEN"), "TCP address to listen on (windows default :port)")
	showVersion := flag.Bool("version", false, "print version information")
	var relays relaySpecs
	flag.Var(&relays, "relay", "TCP relay: vsockPort:tcpAddr (e.g. 2222:localhost:22)")
	flag.Parse()

	if *showVersion {
		fmt.Println(agentVersionInfo())
		return
	}

	// Auto-detect mode from launchd label if not specified.
	if *mode == "" {
		*mode = detectMode()
	}

	// Resolve port from mode if not explicitly set.
	if *port == 0 {
		switch *mode {
		case "agent":
			*port = agentPort
		default:
			*port = daemonPort
		}
	}

	component := "vz-agent"
	if *mode == "agent" {
		component = "vz-agent[user]"
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With(slog.String("component", component))
	slog.SetDefault(logger)

	slog.Info("starting",
		slog.String("version", agentVersionInfo()),
		slog.String("mode", *mode),
		slog.Int("port", *port),
	)

	lis, err := listenAgent(uint32(*port), *tcpListen)
	if err != nil {
		slog.Error("listen", slog.Any("err", err))
		os.Exit(1)
	}
	defer lis.Close()

	mux := http.NewServeMux()
	switch *mode {
	case "agent":
		path, handler := agentpbconnect.NewUserAgentHandler(newUserAgentServer())
		mux.Handle(path, handler)
	default:
		path, handler := agentpbconnect.NewAgentHandler(newAgentServer())
		mux.Handle(path, handler)
	}

	h2cHandler := h2c.NewHandler(mux, &http2.Server{})
	srv := &http.Server{Handler: h2cHandler}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		srv.Shutdown(context.Background())
	}()

	// iTerm2 relay only runs in agent mode (needs user session for iTerm2).
	if *mode == "agent" {
		go startITerm2Relay(ctx)
	}

	// Start configured TCP relays.
	for _, spec := range relays {
		vport, addr, err := parseRelaySpec(spec)
		if err != nil {
			slog.Error("invalid relay spec", slog.String("spec", spec), slog.Any("err", err))
			continue
		}
		if _, err := StartTCPRelay(ctx, vport, addr); err != nil {
			slog.Error("start relay", slog.String("spec", spec), slog.Any("err", err))
		}
	}

	slog.Info("listening",
		slog.Int("port", *port),
		slog.String("addr", lis.Addr().String()),
		slog.String("network", lis.Addr().Network()),
	)
	if err := srv.Serve(lis); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", slog.Any("err", err))
		os.Exit(1)
	}
}

// detectMode infers the mode from the XPC_SERVICE_NAME environment variable
// set by launchd, or falls back to "daemon".
func detectMode() string {
	label := os.Getenv("XPC_SERVICE_NAME")
	if strings.HasSuffix(label, "-user") || strings.Contains(label, ".user.") {
		return "agent"
	}
	return "daemon"
}

// parseRelaySpec parses "vsockPort:host:port" or "vsockPort:port" into
// a vsock port number and TCP address string.
func parseRelaySpec(spec string) (uint32, string, error) {
	// Split on first colon only to get vsockPort and tcpAddr.
	idx := strings.IndexByte(spec, ':')
	if idx < 0 {
		return 0, "", fmt.Errorf("expected vsockPort:tcpAddr")
	}
	vportStr := spec[:idx]
	addr := spec[idx+1:]

	vport, err := strconv.ParseUint(vportStr, 10, 32)
	if err != nil {
		return 0, "", fmt.Errorf("invalid vsock port %q: %w", vportStr, err)
	}

	// If addr is just a port number, prefix with localhost.
	if _, err := strconv.ParseUint(addr, 10, 16); err == nil {
		addr = "localhost:" + addr
	}

	return uint32(vport), addr, nil
}
