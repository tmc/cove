// cove-fleetd is the fleet control-plane process.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tmc/cove/internal/fleetcontrol"
	buildversion "github.com/tmc/cove/internal/version"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9758", "HTTP listen address")
	storePath := flag.String("store", defaultStorePath(), "host inventory store path; empty keeps memory only")
	workerTTL := flag.Duration("worker-ttl", fleetcontrol.DefaultWorkerTTL, "duration before a worker heartbeat is stale")
	assignmentTTL := flag.Duration("assignment-ttl", fleetcontrol.DefaultAssignmentTTL, "duration before a leased assignment can be reclaimed")
	reconcileInterval := flag.Duration("reconcile-interval", 5*time.Second, "duration between controller reconciliation passes; zero disables the loop")
	showVersion := flag.Bool("version", false, "print version information")
	flag.Parse()

	info := buildversion.Resolve(version, commit, date)
	if *showVersion {
		fmt.Println(buildversion.Format("cove-fleetd", info))
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With(slog.String("component", "cove-fleetd"))
	slog.SetDefault(logger)

	store, err := fleetcontrol.OpenStore(*storePath, *workerTTL)
	if err != nil {
		logger.Error("open store", slog.Any("err", err))
		os.Exit(1)
	}
	store.SetAssignmentTTL(*assignmentTTL)
	server := &http.Server{
		Addr:              *addr,
		Handler:           fleetcontrol.Handler(store),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runReconcileLoop(ctx, store, *reconcileInterval, logger)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", slog.String("addr", *addr), slog.String("store", *storePath))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("serve", slog.Any("err", err))
		os.Exit(1)
	}
}

func runReconcileLoop(ctx context.Context, store *fleetcontrol.Store, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := store.Reconcile()
			if err != nil {
				logger.Warn("reconcile", slog.Any("err", err))
				continue
			}
			if len(result.StaleWorkers) > 0 || len(result.RequeuedAssignments) > 0 || len(result.ReplacedAssignments) > 0 {
				logger.Info("reconciled",
					slog.Int("stale_workers", len(result.StaleWorkers)),
					slog.Int("requeued_assignments", len(result.RequeuedAssignments)),
					slog.Int("replaced_assignments", len(result.ReplacedAssignments)),
				)
			}
		}
	}
}

func defaultStorePath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".vz", "fleet-controller.json")
}
