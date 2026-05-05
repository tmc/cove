// coved is the host-side cove coordinator daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	buildversion "github.com/tmc/vz-macos/internal/version"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	socketPath := flag.String("socket", defaultSocketPath(), "unix socket path")
	pidPath := flag.String("pid", defaultPIDPath(), "pid file path")
	showVersion := flag.Bool("version", false, "print version information")
	flag.Parse()

	info := buildversion.Resolve(version, commit, date)
	if *showVersion {
		fmt.Println(buildversion.Format("coved", info))
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With(slog.String("component", "coved"))
	slog.SetDefault(logger)

	if err := os.MkdirAll(filepath.Dir(*socketPath), 0700); err != nil {
		slog.Error("create socket dir", slog.Any("err", err))
		os.Exit(1)
	}
	if err := os.Remove(*socketPath); err != nil && !os.IsNotExist(err) {
		slog.Error("remove stale socket", slog.Any("err", err))
		os.Exit(1)
	}
	l, err := net.Listen("unix", *socketPath)
	if err != nil {
		slog.Error("listen", slog.Any("err", err))
		os.Exit(1)
	}
	defer l.Close()
	defer os.Remove(*socketPath)

	if err := writePIDFile(*pidPath); err != nil {
		slog.Error("write pid", slog.Any("err", err))
		os.Exit(1)
	}
	defer os.Remove(*pidPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d := &daemon{
		version:   buildversion.Host(info),
		started:   time.Now(),
		vmRoot:    vmconfig.BaseDir(),
		socket:    *socketPath,
		pidPath:   *pidPath,
		connected: make(chan struct{}),
	}
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	slog.Info("listening", slog.String("socket", *socketPath))
	if err := d.serve(ctx, l); err != nil && ctx.Err() == nil {
		slog.Error("serve", slog.Any("err", err))
		os.Exit(1)
	}
}

func defaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "cove.sock")
}

func defaultPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "cove.pid")
}

func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
}
