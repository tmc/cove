package main

import (
	"errors"
	"reflect"
	"testing"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/vmrun"
)

type recordedMacStartOptions struct {
	calls  []bool
	failAt int
	err    error
}

func (o *recordedMacStartOptions) set(value bool) error {
	o.calls = append(o.calls, value)
	if len(o.calls) == o.failAt {
		return o.err
	}
	return nil
}
func (o *recordedMacStartOptions) SetForceDFU(v bool) error          { return o.set(v) }
func (o *recordedMacStartOptions) SetStopInIBootStage1(v bool) error { return o.set(v) }
func (o *recordedMacStartOptions) SetStopInIBootStage2(v bool) error { return o.set(v) }

func TestPrivateMacStartOptionsErrors(t *testing.T) {
	sentinel := errors.New("selector unavailable")
	for _, tt := range []struct {
		name   string
		failAt int
		want   []bool
	}{
		{"dfu", 1, []bool{true}},
		{"stage1", 2, []bool{true, false}},
		{"stage2", 3, []bool{true, false, false}},
		{"success", 0, []bool{true, false, false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts := &recordedMacStartOptions{failAt: tt.failAt, err: sentinel}
			err := applyPrivateMacStartOptions(opts, vmrun.RunConfig{OS: vmrun.GuestIOS, ForceDFU: true})
			if tt.failAt == 0 && err != nil || tt.failAt != 0 && !errors.Is(err, sentinel) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(opts.calls, tt.want) {
				t.Fatalf("calls = %v, want %v", opts.calls, tt.want)
			}
		})
	}
}

func TestMacStartOptionsRequired(t *testing.T) {
	for _, tt := range []struct {
		name          string
		rc            vmrun.RunConfig
		want, wantErr bool
	}{
		{"ios normal", vmrun.RunConfig{OS: vmrun.GuestIOS}, false, false},
		{"ios dfu", vmrun.RunConfig{OS: vmrun.GuestIOS, ForceDFU: true}, true, false},
		{"ios stage1", vmrun.RunConfig{OS: vmrun.GuestIOS, StopIBoot1: true}, true, false},
		{"ios stage2", vmrun.RunConfig{OS: vmrun.GuestIOS, StopIBoot2: true}, true, false},
		{"ios recovery", vmrun.RunConfig{OS: vmrun.GuestIOS, RecoveryMode: true}, false, true},
		{"macos recovery", vmrun.RunConfig{OS: vmrun.GuestMacOS, RecoveryMode: true}, true, false},
		{"macos dfu", vmrun.RunConfig{OS: vmrun.GuestMacOS, ForceDFU: true}, true, false},
		{"linux dfu", vmrun.RunConfig{OS: vmrun.GuestLinux, ForceDFU: true}, false, true},
		{"windows recovery", vmrun.RunConfig{OS: vmrun.GuestWindows, RecoveryMode: true}, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := macStartOptionsRequired(tt.rc)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("required = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestIOSRecoveryStartFailsBeforeNativeCall(t *testing.T) {
	calls := 0
	startVMWithRunConfig(vz.VZVirtualMachine{}, vmrun.RunConfig{OS: vmrun.GuestIOS, RecoveryMode: true}, func(err error) {
		calls++
		if err == nil {
			t.Error("recovery accepted for ios")
		}
	})
	if calls != 1 {
		t.Fatalf("completion called %d times", calls)
	}
}
