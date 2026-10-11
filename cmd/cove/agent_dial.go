package main

import (
	"context"
	"fmt"
	"net"
	"sync"

	agentstate "github.com/tmc/cove/internal/agent"
)

func (s *ControlServer) dialAgentPort(ctx context.Context, port uint32, connect func() (net.Conn, error)) (net.Conn, error) {
	switch port {
	case agentstate.DaemonPort:
		return s.agentDial.dial(ctx, connect)
	case agentstate.UserPort:
		return s.userAgentDial.dial(ctx, connect)
	default:
		return nil, fmt.Errorf("unsupported agent port %d", port)
	}
}

type agentDialObserverKey struct{}

func observeAgentDial(ctx context.Context, phase string) {
	if observe, ok := ctx.Value(agentDialObserverKey{}).(func(string)); ok {
		observe(phase)
	}
}

type agentDialGate struct {
	once sync.Once
	slot chan struct{}
}

func (g *agentDialGate) dial(ctx context.Context, connect func() (net.Conn, error)) (net.Conn, error) {
	g.once.Do(func() { g.slot = make(chan struct{}, 1) })
	observeAgentDial(ctx, "native-slot-wait")
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-g.slot
		return nil, err
	}
	observeAgentDial(ctx, "native-slot-admitted")

	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result)
	// The native API cannot cancel a pending connection. Keep the slot until
	// it completes, so retries cannot accumulate pending operations or workers.
	go func() {
		defer func() { <-g.slot }()
		observeAgentDial(ctx, "native-connect-start")
		conn, err := connect()
		observeAgentDial(ctx, "native-connect-return")
		select {
		case done <- result{conn, err}:
		case <-ctx.Done():
			if conn != nil {
				conn.Close()
				observeAgentDial(ctx, "native-late-connection-closed")
			}
		}
	}()
	select {
	case r := <-done:
		if err := ctx.Err(); err != nil {
			if r.conn != nil {
				r.conn.Close()
			}
			return nil, err
		}
		return r.conn, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
