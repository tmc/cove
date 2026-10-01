package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func (s *ControlServer) handleCopyStream(conn net.Conn, req *controlpb.ControlRequest) {
	serveCopyStream(s.lifecycleContext(), conn, func(ctx context.Context) *controlpb.ControlResponse {
		cmd := req.GetAgentCp()
		if cmd == nil {
			return &controlpb.ControlResponse{Error: "missing copy command"}
		}
		return s.handleAgentCopyContext(ctx, cmd)
	})
}

func serveCopyStream(parent context.Context, conn net.Conn, copyFile func(context.Context) *controlpb.ControlResponse) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var transferred, total atomic.Int64
	ctx = agentstate.WithCopyProgress(ctx, func(bytes, size int64) { transferred.Store(bytes); total.Store(size) })
	// The streaming connection is dedicated to one transfer. Closing it cancels
	// the backend RPC, including when the caller presses Ctrl-C or Cancel.
	go func() { _, _ = io.Copy(io.Discard, conn); cancel() }()
	done := make(chan *controlpb.ControlResponse, 1)
	go func() { done <- copyFile(ctx) }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	start := time.Now()
	report := func(status string) error {
		data, _ := json.Marshal(map[string]string{"copyProgress": status})
		return writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(data)})
	}
	if report("Copying…") != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case resp := <-done:
			_ = writeResponse(conn, resp)
			return
		case <-ticker.C:
			status := fmt.Sprintf("Copying… %s elapsed", time.Since(start).Round(time.Second))
			if n := transferred.Load(); n > 0 {
				status += fmt.Sprintf("; %d bytes transferred", n)
				if size := total.Load(); size > 0 {
					status += fmt.Sprintf(" of %d", size)
				}
			}
			if report(status) != nil {
				return
			}
		}
	}
}
