package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	vsockx "github.com/tmc/apple/x/vzkit/vsock"
)

func TestVsockManagerQueueConstruction(t *testing.T) {
	queue := make(chan func(), 1)
	created := false
	mgr := new(vsockx.Manager)
	result := make(chan *VsockDeviceManager, 1)
	go func() {
		got, err := newVsockDeviceManager(func(fn func()) { queue <- fn }, func() (*vsockx.Manager, error) {
			created = true
			return mgr, nil
		})
		if err != nil {
			t.Error(err)
		}
		result <- got
	}()
	var work func()
	select {
	case work = <-queue:
	case <-time.After(time.Second):
		t.Fatal("constructor did not queue device lookup")
	}
	var once sync.Once
	runWork := func() { once.Do(work) }
	defer runWork()
	if created {
		t.Fatal("read VM devices before queue dispatched work")
	}
	select {
	case <-result:
		t.Fatal("constructor returned before queued device lookup")
	default:
	}
	runWork()
	select {
	case got := <-result:
		if got == nil || got.mgr != mgr || !created {
			t.Fatalf("manager %v, created %v", got, created)
		}
	case <-time.After(time.Second):
		t.Fatal("constructor did not finish")
	}
	called := false
	mgr.DispatchFunc(func() { called = true })
	if called {
		t.Fatal("connection dispatcher bypassed queue")
	}
	select {
	case work := <-queue:
		work()
	case <-time.After(time.Second):
		t.Fatal("connection dispatcher did not enqueue work")
	}
	if !called {
		t.Fatal("connection dispatcher did not run queued work")
	}
}

func TestVsockManagerQueueError(t *testing.T) {
	want := errors.New("no socket devices")
	got, err := newVsockDeviceManager(func(fn func()) { fn() }, func() (*vsockx.Manager, error) { return nil, want })
	if got != nil || !errors.Is(err, want) {
		t.Fatalf("manager %v, error %v", got, err)
	}
}

func TestVsockManagerInvalidOwner(t *testing.T) {
	for _, tt := range []struct {
		name string
		vm   vz.VZVirtualMachine
	}{
		{"missing-vm", vz.VZVirtualMachine{}},
		{"missing-queue", vz.VZVirtualMachineFromID(1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := NewVsockDeviceManager(tt.vm, dispatch.Queue{}); got != nil || err == nil {
				t.Fatalf("manager %v, error %v", got, err)
			}
		})
	}
}
