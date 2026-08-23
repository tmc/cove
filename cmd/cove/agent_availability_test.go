package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
	agentstate "github.com/tmc/cove/internal/agent"
)

func TestShouldWarnAgentUnavailable(t *testing.T) {
	tests := []struct {
		name               string
		connected          bool
		previouslyVerified bool
		want               bool
	}{
		{"connected, never verified", true, false, false},
		{"connected, verified before", true, true, false},
		{"unreachable, verified before", false, true, false},
		{"unreachable, never verified", false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldWarnAgentUnavailable(tt.connected, tt.previouslyVerified); got != tt.want {
				t.Errorf("shouldWarnAgentUnavailable(%v, %v) = %v, want %v",
					tt.connected, tt.previouslyVerified, got, tt.want)
			}
		})
	}
}

type fakeAvailabilityTarget struct {
	mu       sync.Mutex
	state    vz.VZVirtualMachineState
	attempts int
	connect  bool
	dir      string
}

func (f *fakeAvailabilityTarget) currentVMState() (vz.VZVirtualMachineState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func (f *fakeAvailabilityTarget) setState(s vz.VZVirtualMachineState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = s
}

func (f *fakeAvailabilityTarget) getAgent() (*agentstate.AgentClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.connect {
		return nil, nil
	}
	return nil, fmt.Errorf("dial vsock: connection refused")
}

func (f *fakeAvailabilityTarget) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

func (f *fakeAvailabilityTarget) effectiveVMDir() string { return f.dir }
func (f *fakeAvailabilityTarget) vmHintFlag() string     { return "" }

func shortenAgentAvailabilityBudget(t *testing.T) {
	t.Helper()
	grace, window, min, max := agentAvailabilityBootGrace, agentAvailabilityWindow, agentAvailabilityMinInterval, agentAvailabilityMaxInterval
	agentAvailabilityBootGrace = time.Millisecond
	agentAvailabilityWindow = 50 * time.Millisecond
	agentAvailabilityMinInterval = time.Millisecond
	agentAvailabilityMaxInterval = 2 * time.Millisecond
	t.Cleanup(func() {
		agentAvailabilityBootGrace, agentAvailabilityWindow = grace, window
		agentAvailabilityMinInterval, agentAvailabilityMaxInterval = min, max
	})
}

func TestCheckAgentAvailabilityStopsWhenVMStops(t *testing.T) {
	shortenAgentAvailabilityBudget(t)
	target := &fakeAvailabilityTarget{state: vz.VZVirtualMachineStateRunning, dir: t.TempDir()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		checkAgentAvailabilityContext(context.Background(), target)
	}()
	time.Sleep(5 * time.Millisecond)
	target.setState(vz.VZVirtualMachineStateStopped)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("checkAgentAvailability did not exit after the VM stopped")
	}
}

func TestCheckAgentAvailabilityStopsOnCancel(t *testing.T) {
	shortenAgentAvailabilityBudget(t)
	agentAvailabilityBootGraceLong(t)

	target := &fakeAvailabilityTarget{state: vz.VZVirtualMachineStateRunning, dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		checkAgentAvailabilityContext(ctx, target)
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("checkAgentAvailability did not exit after cancellation")
	}
}

// agentAvailabilityBootGraceLong makes the boot grace long enough that the
// cancellation test is exercised while the goroutine is still waiting.
func agentAvailabilityBootGraceLong(t *testing.T) {
	t.Helper()
	prev := agentAvailabilityBootGrace
	agentAvailabilityBootGrace = time.Minute
	t.Cleanup(func() { agentAvailabilityBootGrace = prev })
}

func TestCheckAgentAvailabilityMarksVerified(t *testing.T) {
	shortenAgentAvailabilityBudget(t)
	dir := t.TempDir()
	target := &fakeAvailabilityTarget{state: vz.VZVirtualMachineStateRunning, connect: true, dir: dir}
	checkAgentAvailabilityContext(context.Background(), target)
	if !agentstate.Verified(dir) {
		t.Fatal("agent state was not marked verified after a successful connection")
	}
	if got := target.attemptCount(); got != 1 {
		t.Fatalf("getAgent attempts = %d, want 1", got)
	}
}

func TestCheckAgentAvailabilityRetriesBeyondThreeAttempts(t *testing.T) {
	shortenAgentAvailabilityBudget(t)
	agentAvailabilityWindowLong(t)
	dir := t.TempDir()
	target := &fakeAvailabilityTarget{state: vz.VZVirtualMachineStateRunning, dir: dir}
	checkAgentAvailabilityContext(context.Background(), target)
	if got := target.attemptCount(); got <= 3 {
		t.Fatalf("getAgent attempts = %d, want more than the old fixed 3", got)
	}
}

func agentAvailabilityWindowLong(t *testing.T) {
	t.Helper()
	prev := agentAvailabilityWindow
	agentAvailabilityWindow = 200 * time.Millisecond
	t.Cleanup(func() { agentAvailabilityWindow = prev })
}

func TestAgentStateVerifiedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if agentstate.Verified(dir) {
		t.Fatal("fresh VM directory reported a verified agent")
	}
	if err := agentstate.MarkVerified(dir, agentstate.PlatformMacOS, agentstate.SourceRuntime, time.Now()); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	if !agentstate.Verified(dir) {
		t.Fatal("Verified reported false after MarkVerified")
	}
	if st := agentstate.Status(dir); st == nil || st.Platform != agentstate.PlatformMacOS {
		t.Fatalf("Status = %+v, want platform %q", st, agentstate.PlatformMacOS)
	}
}
