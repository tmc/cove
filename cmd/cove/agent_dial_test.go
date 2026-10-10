package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
)

func TestAgentDialPortsRemainIndependent(t *testing.T) {
	server := &ControlServer{}
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := server.dialAgentPort(ctx, agentstate.UserPort, func() (net.Conn, error) {
			close(started)
			<-release
			return nil, nil
		})
		finished <- err
	}()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("user dial = %v", err)
	}
	rootCtx, rootCancel := context.WithTimeout(context.Background(), time.Second)
	defer rootCancel()
	want := errors.New("root connection attempted")
	_, err := server.dialAgentPort(rootCtx, agentstate.DaemonPort, func() (net.Conn, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("root dial with pending user connection = %v", err)
	}
	_, err = server.dialAgentPort(rootCtx, 9000, func() (net.Conn, error) {
		t.Error("started an unsupported port connection")
		return nil, nil
	})
	if err == nil {
		t.Fatal("unsupported agent port accepted")
	}
}

type agentDialTestConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *agentDialTestConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestAgentDialHungOperation(t *testing.T) {
	var gate agentDialGate
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseOperation := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseOperation()
	left, right := net.Pipe()
	defer right.Close()
	conn := &agentDialTestConn{Conn: left, closed: make(chan struct{})}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := gate.dial(ctx, func() (net.Conn, error) {
			calls.Add(1)
			close(started)
			<-release
			return conn, nil
		})
		finished <- err
	}()
	<-started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("dial error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for native completion")
	}
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_, err := gate.dial(ctx, func() (net.Conn, error) {
			calls.Add(1)
			return nil, nil
		})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("retry error = %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("native operations = %d, want 1", got)
	}
	releaseOperation()
	select {
	case <-conn.closed:
	case <-time.After(time.Second):
		t.Fatal("late connection was not closed")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := errors.New("new native operation")
	_, err := gate.dial(ctx, func() (net.Conn, error) {
		calls.Add(1)
		return nil, want
	})
	if !errors.Is(err, want) || calls.Load() != 2 {
		t.Fatalf("after completion: error %v, calls %d", err, calls.Load())
	}
}

func TestAgentDialCanceledBeforeStart(t *testing.T) {
	var gate agentDialGate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gate.dial(ctx, func() (net.Conn, error) {
		t.Error("started native operation with canceled context")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestAgentDialCancellationAtCompletion(t *testing.T) {
	var gate agentDialGate
	left, right := net.Pipe()
	defer right.Close()
	conn := &agentDialTestConn{Conn: left, closed: make(chan struct{})}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, err := gate.dial(ctx, func() (net.Conn, error) {
		cancel()
		return conn, nil
	})
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("connection %v, error %v", got, err)
	}
	select {
	case <-conn.closed:
	case <-time.After(time.Second):
		t.Fatal("connection delivered during cancellation was not closed")
	}
}

func TestAgentDialSuccess(t *testing.T) {
	var gate agentDialGate
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	got, err := gate.dial(context.Background(), func() (net.Conn, error) { return left, nil })
	if got != left || err != nil {
		t.Fatalf("connection %v, error %v", got, err)
	}
}
