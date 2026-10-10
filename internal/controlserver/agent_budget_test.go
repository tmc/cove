package controlserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
	agentstate "github.com/tmc/cove/internal/agent"
)

func ExampleAgentBridge_GetAgentContext() {
	var bridge AgentBridge
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := bridge.GetAgentContext(ctx)
	fmt.Println(errors.Is(err, context.Canceled))
	// Output: true
}

func ExampleAgentBridge_GetUserAgentContext() {
	var bridge AgentBridge
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := bridge.GetUserAgentContext(ctx)
	fmt.Println(errors.Is(err, context.Canceled))
	// Output: true
}

type budgetAgentHost struct{ AgentHost }

func (budgetAgentHost) VMState() (vz.VZVirtualMachineState, error) {
	return vz.VZVirtualMachineStateRunning, nil
}
func (budgetAgentHost) LifecycleContext() context.Context { return context.Background() }

func TestAgentBudgetCanceledProbeKeepsCachedClient(t *testing.T) {
	var closed atomic.Bool
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	client, err := agentstate.NewUserAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}, func() { closed.Store(true) })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	b := &AgentBridge{host: budgetAgentHost{}, userAgent: client}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := b.GetUserAgentContext(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled probe did not return")
	}
	if closed.Load() || b.userAgent != client || calls.Load() != 1 {
		t.Fatalf("closed %v, cached %v, calls %d", closed.Load(), b.userAgent == client, calls.Load())
	}
}

func TestAgentBudgetBlockedAcquisitionKeepsLock(t *testing.T) {
	b := &AgentBridge{host: budgetAgentHost{}}
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := b.GetUserAgentContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if b.mu.TryLock() {
		b.mu.Unlock()
		t.Fatal("waiter released lock owner")
	}
}

func TestAgentBudgetExpiredBeforeAcquisition(t *testing.T) {
	b := new(AgentBridge)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.GetUserAgentContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("user acquisition: %v", err)
	}
	if _, err := b.GetAgentContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("root acquisition: %v", err)
	}
}

func TestAgentBudgetLockCancellationPreservesOwner(t *testing.T) {
	for _, read := range []bool{false, true} {
		b := new(AgentBridge)
		b.mu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		err := b.lockAgentContext(ctx, read)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			b.mu.Unlock()
			t.Fatalf("lock: %v", err)
		}
		if b.mu.TryLock() {
			b.mu.Unlock()
			t.Fatal("canceled waiter released mutator lock")
		}
		b.mu.Unlock()
	}
}
