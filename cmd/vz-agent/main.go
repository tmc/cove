// vz-agent is a guest agent daemon for VMs managed by vz-macos.
// It runs inside the guest (macOS or Linux) and exposes a connect-go API over vsock.
//
// The host connects to the agent via VZVirtioSocketDevice on port 1024
// to execute commands, transfer files, and manage the guest.
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
	"syscall"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tmc/vz-macos/proto/agentpbconnect"
)

const defaultPort = 1024

func main() {
	port := flag.Int("port", defaultPort, "vsock port to listen on")
	showVersion := flag.Bool("version", false, "print version information")
	flag.Parse()

	if *showVersion {
		fmt.Println(agentVersionInfo())
		return
	}

	log.SetPrefix("vz-agent: ")
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Printf("starting version %s on vsock port %d", agentVersion(), *port)

	lis, err := listenVsock(uint32(*port))
	if err != nil {
		log.Fatalf("listen vsock: %v", err)
	}
	defer lis.Close()

	mux := http.NewServeMux()
	path, handler := agentpbconnect.NewAgentHandler(newAgentServer())
	mux.Handle(path, handler)

	h2cHandler := h2c.NewHandler(mux, &http2.Server{})
	srv := &http.Server{Handler: h2cHandler}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		log.Println("shutting down")
		srv.Shutdown(context.Background())
	}()

	log.Printf("listening on vsock port %d", *port)
	if err := srv.Serve(lis); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
