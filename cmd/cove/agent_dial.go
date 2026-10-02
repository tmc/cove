package main

import (
	"context"
	"net"
	"sync"
)

type agentDialGate struct {
	once sync.Once
	slot chan struct{}
}

func (g *agentDialGate) dial(ctx context.Context, connect func() (net.Conn, error)) (net.Conn, error) {
	g.once.Do(func() { g.slot = make(chan struct{}, 1) })
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-g.slot
		return nil, err
	}
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result)
	// The native API cannot cancel a pending connection. Keep the slot until
	// it completes, so retries cannot accumulate pending operations or workers.
	go func() {
		defer func() { <-g.slot }()
		conn, err := connect()
		select {
		case done <- result{conn, err}:
		case <-ctx.Done():
			if conn != nil {
				conn.Close()
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
