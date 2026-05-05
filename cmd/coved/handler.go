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
)

type daemon struct {
	version   string
	started   time.Time
	vmRoot    string
	socket    string
	pidPath   string
	connected chan struct{}
}

type statusResponse struct {
	Version    string `json:"version"`
	UptimeS    int64  `json:"uptime_s"`
	VMsManaged int    `json:"vms_managed"`
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
	return statusResponse{
		Version:    d.version,
		UptimeS:    int64(time.Since(d.started).Seconds()),
		VMsManaged: countVMDirs(d.vmRoot),
	}
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
