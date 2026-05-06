// coved is the host-side cove coordinator daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tmc/vz-macos/internal/coved"
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
	configPath := flag.String("config", coved.DefaultConfigPath(), "config file path")
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
	cfg, err := coved.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load config", slog.Any("err", err))
		os.Exit(1)
	}

	d := &daemon{
		version:   buildversion.Host(info),
		started:   time.Now(),
		vmRoot:    vmconfig.BaseDir(),
		socket:    *socketPath,
		pidPath:   *pidPath,
		connected: make(chan struct{}),
		events:    coved.NewEventBus(50),
	}
	gc := coved.NewImageGCScheduler("", logger)
	gc.Bus = d.events
	d.imageGC = gc
	if sink, err := coved.NewJSONLSink(""); err == nil {
		go coved.RunJSONLSubscriber(ctx, d.events, sink)
	} else {
		slog.Debug("metrics jsonl subscriber", slog.Any("err", err))
	}
	if webhook := coved.NewWebhookSubscriber(cfg.Daemon.Webhook); webhook != nil {
		go webhook.Run(ctx, d.events)
	}
	go gc.RunScheduledImageGC(ctx)
	lifecycle := coved.NewLifecycleEnforcer(coved.LifecycleConfig{
		VMRoot: d.vmRoot,
		Log:    logger,
		Bus:    d.events,
	})
	d.lifecycle = lifecycle
	go lifecycle.Run(ctx)
	if cfg.Daemon.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", coved.PrometheusHandler(d.prometheusSnapshot))
		d.http = &http.Server{Addr: cfg.Daemon.MetricsAddr, Handler: mux}
		go func() {
			if err := d.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics http", slog.Any("err", err))
			}
		}()
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = d.http.Shutdown(shutdownCtx)
		}()
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
