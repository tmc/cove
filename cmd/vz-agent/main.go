// vz-agent is a guest agent daemon for VMs managed by vz-macos.
// It runs inside the guest (macOS or Linux) and exposes a GRPC API over vsock.
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
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	pb "github.com/tmc/vz-macos/proto/agentpb"
)

const defaultPort = 1024

func agentVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		v := info.Main.Version
		if v != "" && v != "(devel)" {
			return v
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 8 {
				return s.Value[:8]
			}
		}
	}
	return "dev"
}

func main() {
	port := flag.Int("port", defaultPort, "vsock port to listen on")
	flag.Parse()

	log.SetPrefix("vz-agent: ")
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Printf("starting version %s on vsock port %d", agentVersion(), *port)

	lis, err := listenVsock(uint32(*port))
	if err != nil {
		log.Fatalf("listen vsock: %v", err)
	}
	defer lis.Close()

	srv := grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             20 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    60 * time.Second,
			Timeout: 30 * time.Second,
		}),
	)
	pb.RegisterAgentServer(srv, newAgentServer())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		log.Println("shutting down")
		srv.GracefulStop()
	}()

	log.Printf("listening on vsock port %d", *port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
