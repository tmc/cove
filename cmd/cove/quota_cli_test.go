package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmquota"
)

func TestParseQuotaArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want quotaCommand
		err  string
	}{
		{name: "show", args: []string{"vm1", "show"}, want: quotaCommand{VM: "vm1", Action: "show"}},
		{name: "cpu", args: []string{"vm1", "cpu", "4"}, want: quotaCommand{VM: "vm1", Action: "cpu", Value: 4}},
		{name: "memory", args: []string{"vm1", "memory", "8"}, want: quotaCommand{VM: "vm1", Action: "memory", Value: 8}},
		{name: "disk", args: []string{"vm1", "disk", "50"}, want: quotaCommand{VM: "vm1", Action: "disk", Value: 50}},
		{name: "missing", args: nil, err: "usage"},
		{name: "slash vm", args: []string{"bad/vm", "show"}, err: "invalid VM name"},
		{name: "unknown", args: []string{"vm1", "gpu", "1"}, err: "unknown action"},
		{name: "show extra", args: []string{"vm1", "show", "x"}, err: "usage"},
		{name: "cpu missing", args: []string{"vm1", "cpu"}, err: "usage"},
		{name: "cpu zero", args: []string{"vm1", "cpu", "0"}, err: "invalid cpu value"},
		{name: "memory bad", args: []string{"vm1", "memory", "bad"}, err: "invalid memory value"},
		{name: "disk negative", args: []string{"vm1", "disk", "-1"}, err: "invalid disk value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseQuotaArgs(tc.args, io.Discard)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("parseQuotaArgs error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseQuotaArgs: %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseQuotaArgs = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRunQuotaShow(t *testing.T) {
	manager := &fakeQuotaManager{quota: vmquota.Quota{CPUs: 4, MemoryGB: 8, DiskGB: 50}}
	var out bytes.Buffer
	if err := runQuota(context.Background(), []string{"vm1", "show"}, manager, &out); err != nil {
		t.Fatalf("runQuota show: %v", err)
	}
	got := out.String()
	for _, want := range []string{"vm: vm1", "cpu: 4", "memory: 8 GB", "disk: 50 GB"} {
		if !strings.Contains(got, want) {
			t.Fatalf("show output missing %q:\n%s", want, got)
		}
	}
}

func TestRunQuotaSetters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
		val  uint64
	}{
		{name: "cpu", args: []string{"vm1", "cpu", "4"}, want: "cpu", val: 4},
		{name: "memory", args: []string{"vm1", "memory", "8"}, want: "memory", val: 8},
		{name: "disk", args: []string{"vm1", "disk", "50"}, want: "disk", val: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &fakeQuotaManager{}
			if err := runQuota(context.Background(), tc.args, manager, &bytes.Buffer{}); err != nil {
				t.Fatalf("runQuota: %v", err)
			}
			if manager.action != tc.want || manager.vm != "vm1" || manager.value != tc.val {
				t.Fatalf("manager got action=%q vm=%q value=%d, want %s vm1 %d", manager.action, manager.vm, manager.value, tc.want, tc.val)
			}
		})
	}
}

func TestRunQuotaPropagatesError(t *testing.T) {
	want := errors.New("boom")
	err := runQuota(context.Background(), []string{"vm1", "disk", "50"}, &fakeQuotaManager{err: want}, &bytes.Buffer{})
	if !errors.Is(err, want) {
		t.Fatalf("runQuota error = %v, want %v", err, want)
	}
}

func TestApplyInstallDiskQuotaIgnoresUnsupportedSetQuota(t *testing.T) {
	oldDiskSize := diskSizeGB
	oldApply := applyAPFSQuotaForInstall
	defer func() {
		diskSizeGB = oldDiskSize
		applyAPFSQuotaForInstall = oldApply
	}()

	diskSizeGB = 64
	applyAPFSQuotaForInstall = func(dir string, gb uint64) error {
		if dir != "/tmp/vm" || gb != 64 {
			return fmt.Errorf("got dir=%q gb=%d", dir, gb)
		}
		return errors.New(`diskutil apfs did not recognize APFS verb "setQuota"; usage: diskutil apfs ...`)
	}
	var out bytes.Buffer
	if err := applyInstallDiskQuota(&out, "/tmp/vm"); err != nil {
		t.Fatalf("applyInstallDiskQuota unsupported setQuota: %v", err)
	}
	if !strings.Contains(out.String(), "APFS directory quotas are not supported") {
		t.Fatalf("output = %q, want unsupported quota warning", out.String())
	}
}

func TestApplyInstallDiskQuotaPropagatesOtherErrors(t *testing.T) {
	oldDiskSize := diskSizeGB
	oldApply := applyAPFSQuotaForInstall
	defer func() {
		diskSizeGB = oldDiskSize
		applyAPFSQuotaForInstall = oldApply
	}()

	want := errors.New("permission denied")
	diskSizeGB = 64
	applyAPFSQuotaForInstall = func(string, uint64) error { return want }
	if err := applyInstallDiskQuota(io.Discard, "/tmp/vm"); !errors.Is(err, want) {
		t.Fatalf("applyInstallDiskQuota error = %v, want %v", err, want)
	}
}

func TestPersistInstallQuotaWritesWarningToWriter(t *testing.T) {
	oldCPU, oldMemory, oldDisk := cpuCount, memoryGB, diskSizeGB
	t.Cleanup(func() {
		cpuCount, memoryGB, diskSizeGB = oldCPU, oldMemory, oldDisk
	})

	cpuCount = 4
	memoryGB = 8
	diskSizeGB = 64
	var out bytes.Buffer
	persistInstallQuota(&out, filepath.Join(t.TempDir(), "missing"))
	if !strings.Contains(out.String(), "warning: save quota config:") {
		t.Fatalf("output = %q, want save warning", out.String())
	}
}

type fakeQuotaManager struct {
	quota  vmquota.Quota
	err    error
	action string
	vm     string
	value  uint64
}

func (f *fakeQuotaManager) Show(ctx context.Context, vm string) (vmquota.Quota, error) {
	f.action = "show"
	f.vm = vm
	return f.quota, f.err
}

func (f *fakeQuotaManager) SetCPU(ctx context.Context, vm string, cpus uint) error {
	f.action = "cpu"
	f.vm = vm
	f.value = uint64(cpus)
	return f.err
}

func (f *fakeQuotaManager) SetMemory(ctx context.Context, vm string, gb uint64) error {
	f.action = "memory"
	f.vm = vm
	f.value = gb
	return f.err
}

func (f *fakeQuotaManager) SetDisk(ctx context.Context, vm string, gb uint64) error {
	f.action = "disk"
	f.vm = vm
	f.value = gb
	return f.err
}
