package main

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

func TestIOSStartupCancellationStopsOnce(t *testing.T) {
	signals := make(chan os.Signal, 1)
	state := vz.VZVirtualMachineStateStarting
	stops := 0
	var reports []string
	started := time.Now()
	err := runIOSLifecycle(func() <-chan error {
		signals <- os.Interrupt
		return make(chan error)
	}, func() <-chan error {
		stops++
		state = vz.VZVirtualMachineStateStopped
		return make(chan error)
	}, func() (vz.VZVirtualMachineState, error) { return state, nil }, signals, time.Hour, time.Second, func() {}, func(s string) { reports = append(reports, s) })
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("startup cancellation took %s", elapsed)
	}
	if stops != 1 {
		t.Fatalf("stop calls = %d", stops)
	}
	if !reflect.DeepEqual(reports, []string{"stopped"}) {
		t.Fatalf("reports = %v", reports)
	}
}

func TestIOSStartCompletionRequiresRunning(t *testing.T) {
	for _, tt := range []struct {
		name    string
		state   vz.VZVirtualMachineState
		wantErr bool
	}{
		{"running", vz.VZVirtualMachineStateRunning, false},
		{"stopped", vz.VZVirtualMachineStateStopped, true},
		{"error", vz.VZVirtualMachineStateError, true},
		{"starting", vz.VZVirtualMachineStateStarting, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := make(chan error, 1)
			result <- nil
			err := waitForIOSStart(result, func() (vz.VZVirtualMachineState, error) { return tt.state, nil }, nil, 40*time.Millisecond, func() {})
			if (err != nil) != tt.wantErr {
				t.Fatalf("start = %v", err)
			}
		})
	}
}

func TestIOSStopPumpsWithoutCallback(t *testing.T) {
	state := vz.VZVirtualMachineStateRunning
	pumps := 0
	err := stopIOSVM(func() <-chan error { return make(chan error) }, func() (vz.VZVirtualMachineState, error) { return state, nil }, time.Second, func() { pumps++; state = vz.VZVirtualMachineStateStopped })
	if err != nil || pumps == 0 {
		t.Fatalf("stop = %v, pumps = %d", err, pumps)
	}
}

func TestIOSCancellationPreservesStopError(t *testing.T) {
	signals := make(chan os.Signal, 1)
	signals <- os.Interrupt
	sentinel := errors.New("stop rejected")
	err := runIOSLifecycle(func() <-chan error { return make(chan error) }, func() <-chan error { ch := make(chan error, 1); ch <- sentinel; return ch }, func() (vz.VZVirtualMachineState, error) { return vz.VZVirtualMachineStateStarting, nil }, signals, time.Hour, 20*time.Millisecond, func() {}, func(string) {})
	if !errors.Is(err, sentinel) {
		t.Fatalf("run = %v", err)
	}
}
