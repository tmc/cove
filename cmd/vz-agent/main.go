// vz-agent is a guest agent daemon for VMs managed by vz-macos.
// It runs inside the guest (macOS or Linux) and exposes a connect-go API over vsock.
//
// Two modes:
//   - daemon (default, port 1024): runs as root LaunchDaemon, system ops
//   - agent (port 1025): runs as logged-in user LaunchAgent, inherits TCC/FDA
//
// Platform-specific vsock listener is in vsock_darwin.go / vsock_linux.go.
// Platform-specific system info is in info_darwin.go / info_linux.go.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tmc/vz-macos/proto/agentpbconnect"
)

const (
	daemonPort = 1024
	agentPort  = 1025
)

func main() {
	mode := flag.String("mode", "", "run mode: daemon (root, port 1024) or agent (user, port 1025)")
	port := flag.Int("port", 0, "vsock port to listen on (overrides mode default)")
	showVersion := flag.Bool("version", false, "print version information")
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

	prefix := "vz-agent"
	if *mode == "agent" {
		prefix = "vz-agent[user]"
	}
	log.SetPrefix(prefix + ": ")
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Printf("starting %s in %s mode on vsock port %d", agentVersionInfo(), *mode, *port)

	lis, err := listenVsock(uint32(*port))
	if err != nil {
		log.Fatalf("listen vsock: %v", err)
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
		log.Println("shutting down")
		srv.Shutdown(context.Background())
	}()

	// iTerm2 relay only runs in agent mode (needs user session for iTerm2).
	if *mode == "agent" {
		go startITerm2Relay()
	}

	log.Printf("listening on vsock port %d", *port)
	if err := srv.Serve(lis); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
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
