package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/coved"
)

type daemon struct {
	version   string
	started   time.Time
	vmRoot    string
	socket    string
	pidPath   string
	imageGC   *coved.ImageGCScheduler
	connected chan struct{}
}

type statusResponse struct {
	Version                 string `json:"version"`
	UptimeS                 int64  `json:"uptime_s"`
	VMsManaged              int    `json:"vms_managed"`
	ImageGCLastRunTS        string `json:"image_gc_last_run_ts,omitempty"`
	ImageGCRunsTotal        int64  `json:"image_gc_runs_total"`
	ImageGCBytesFreedTotal  int64  `json:"image_gc_bytes_freed_total"`
	ImageGCManifestsScanned int    `json:"image_gc_manifests_scanned,omitempty"`
	ImageGCManifestsRemoved int    `json:"image_gc_manifests_removed,omitempty"`
}

func (d *daemon) serve(ctx context.Context, l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go d.handle(conn)
	}
}

func (d *daemon) handle(conn net.Conn) {
	defer conn.Close()
	select {
	case <-d.connected:
	default:
		close(d.connected)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintf(conn, `{"error":"read command: %v"}`+"\n", err)
		return
	}
	switch strings.TrimSpace(line) {
	case "STATUS":
		_ = json.NewEncoder(conn).Encode(d.status())
	default:
		fmt.Fprintln(conn, `{"error":"unknown command"}`)
	}
}

func (d *daemon) status() statusResponse {
	resp := statusResponse{
		Version:    d.version,
		UptimeS:    int64(time.Since(d.started).Seconds()),
		VMsManaged: countVMDirs(d.vmRoot),
	}
	if d.imageGC != nil {
		stats, last, runs, bytesFreed := d.imageGC.Stats()
		resp.ImageGCRunsTotal = runs
		resp.ImageGCBytesFreedTotal = bytesFreed
		resp.ImageGCManifestsScanned = stats.ManifestsScanned
		resp.ImageGCManifestsRemoved = stats.ManifestsRemoved
		if !last.IsZero() {
			resp.ImageGCLastRunTS = last.UTC().Format(time.RFC3339Nano)
		}
	}
	return resp
}

func countVMDirs(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	var n int
	for _, entry := range entries {
		if entry.IsDir() {
			n++
		}
	}
	return n
}
