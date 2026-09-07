//go:build darwin

package ios

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/exp/research"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"
)

// Session owns a headless research VM and its serial pipes. Its zero value is
// closed. Call all methods on the main thread while holding the bundle run lock.
// Release that lock only after Close succeeds, or when the process exits.
type Session struct {
	graph      *deviceGraph
	machine    vz.VZVirtualMachine
	serialDone chan error
}

// StartOptions controls research boot. Zero values request ordinary boot.
type StartOptions struct {
	ForceDFU          bool
	StopInIBootStage1 bool
	StopInIBootStage2 bool
}

// OpenSession constructs and validates a graph without starting the VM.
// initialize permits creation of an absent identity; debugPort zero selects an
// automatic AP debug port. output receives serial bytes and must not block
// indefinitely. The caller must eventually stop and close the session.
func OpenSession(ctx context.Context, dir string, initialize bool, debugPort uint16, output io.Writer) (_ *Session, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !foundation.GetThreadClass().CurrentThread().IsMainThread() {
		return nil, fmt.Errorf("ios session requires the main thread")
	}
	if output == nil {
		output = io.Discard
	}
	var session *Session
	objc.AutoreleasePool(func() {
		graph, buildErr := buildGraph(dir, initialize, debugPort)
		if buildErr != nil {
			err = buildErr
			return
		}
		machine := vz.NewVirtualMachineWithConfiguration(graph.configuration)
		if machine.ID == 0 {
			graph.close()
			err = fmt.Errorf("create ios vm: nil object")
			return
		}
		session = &Session{graph: graph, machine: machine, serialDone: make(chan error, 1)}
	})
	if err != nil {
		return nil, err
	}
	go func() {
		_, err := io.Copy(output, session.graph.serialOutput)
		session.serialDone <- err
	}()
	return session, nil
}

// ECID returns the machine identifier's ECID, or zero for a closed session.
func (s *Session) ECID() uint64 {
	if s == nil || s.graph == nil {
		return 0
	}
	return s.graph.ecid
}

// Start starts the VM and pumps the main run loop until completion or timeout.
// A canceled wait does not cancel VZ's asynchronous start. Stop and Close are
// still required, and Close rejects a VM that has not reached a terminal state.
func (s *Session) Start(ctx context.Context, options StartOptions) error {
	if err := s.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.machine.State() != vz.VZVirtualMachineStateStopped {
		return fmt.Errorf("ios vm is not stopped")
	}
	done := make(chan error, 1)
	var setupErr error
	objc.AutoreleasePool(func() {
		start := vz.NewVZMacOSVirtualMachineStartOptions()
		if start.ID == 0 {
			setupErr = fmt.Errorf("create ios start options: nil object")
			return
		}
		defer start.Release()
		if err := research.ConfigureStart(start, research.BootOptions{
			ForceDFU: options.ForceDFU, StopInIBootStage1: options.StopInIBootStage1, StopInIBootStage2: options.StopInIBootStage2,
		}); err != nil {
			setupErr = err
			return
		}

		// The generated convenience method discards NewErrorBlock's cleanup.
		block, release := vz.NewErrorBlock(func(err error) { done <- err })
		defer release()
		objc.Send[struct{}](s.machine.ID, objc.Sel("startWithOptions:completionHandler:"), start, block)
	})
	if setupErr != nil {
		return setupErr
	}
	if err := waitMain(ctx, done); err != nil {
		return fmt.Errorf("start ios vm: %w", err)
	}
	return nil
}

// Wait pumps the main run loop until the VM stops, fails, serial output fails,
// or ctx is canceled. It does not request shutdown when ctx is canceled.
func (s *Session) Wait(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	for {
		switch s.machine.State() {
		case vz.VZVirtualMachineStateStopped:
			return nil
		case vz.VZVirtualMachineStateError:
			return fmt.Errorf("ios vm entered error state")
		}
		select {
		case err := <-s.serialDone:
			s.serialDone <- err
			if err != nil {
				return fmt.Errorf("ios serial output: %w", err)
			}
			return fmt.Errorf("ios serial output closed while vm is active")
		default:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pumpMain()
	}
}

// Stop waits for startup to settle if necessary, then requests an immediate VZ
// stop and waits for completion. A timeout leaves ownership with the caller.
func (s *Session) Stop(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	for {
		state := s.machine.State()
		if state == vz.VZVirtualMachineStateStopped || state == vz.VZVirtualMachineStateError {
			return nil
		}
		if s.machine.CanStop() {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pumpMain()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	block, release := vz.NewErrorBlock(func(err error) { done <- err })
	defer release()
	objc.Send[struct{}](s.machine.ID, objc.Sel("stopWithCompletionHandler:"), block)
	if err := waitMain(ctx, done); err != nil {
		return fmt.Errorf("stop ios vm: %w", err)
	}
	return nil
}

// Close releases a stopped or failed VM, drains serial output within ctx, and
// releases its pipes. It is idempotent; a timed-out drain can be retried.
// On error, the session and its bundle run lock must remain owned by the caller.
func (s *Session) Close(ctx context.Context) error {
	if s == nil || s.graph == nil {
		return nil
	}
	if !foundation.GetThreadClass().CurrentThread().IsMainThread() {
		return fmt.Errorf("ios session requires the main thread")
	}
	if s.machine.ID != 0 {
		state := s.machine.State()
		if state != vz.VZVirtualMachineStateStopped && state != vz.VZVirtualMachineStateError {
			return fmt.Errorf("cannot close active ios vm")
		}
		s.machine.Release()
		s.machine = vz.VZVirtualMachine{}
	}
	return s.closeSerial(ctx)
}

func (s *Session) check() error {
	if s == nil || s.graph == nil || s.machine.ID == 0 {
		return fmt.Errorf("ios session is closed")
	}
	if !foundation.GetThreadClass().CurrentThread().IsMainThread() {
		return fmt.Errorf("ios session requires the main thread")
	}
	return nil
}

func waitMain(ctx context.Context, done <-chan error) error {
	for {
		select {
		case err := <-done:
			return err
		default:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pumpMain()
	}
}

func pumpMain() {
	objc.AutoreleasePool(func() { vmruntime.RunLoopOnce() })
	// A run loop with no sources may return immediately.
	time.Sleep(time.Millisecond)
}

func (s *Session) closeSerial(ctx context.Context) error {
	if s.graph.serialOutputWriter != nil {
		s.graph.serialOutputWriter.Close()
	}
	select {
	case <-s.serialDone:
	default:
		select {
		case <-s.serialDone:
		case <-ctx.Done():
			return fmt.Errorf("drain ios serial output: %w", ctx.Err())
		}
	}
	s.graph.close()
	s.graph = nil
	return nil
}
