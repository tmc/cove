package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"errors"
	"flag"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/iosbundle"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
)

func TestMacOSRunnerRejectsIOSBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 8, MemoryGB: 8, IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = runMacOSVMWithConfig(vmrun.RunConfig{OS: vmrun.GuestMacOS}, vmrun.HostConfig{VMDir: dir}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "ios bundle requires") {
		t.Fatalf("run = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		// vmconfig.Save leaves its lock file behind; it is not guest state.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) != 1 || names[0] != "config.json" {
		t.Fatalf("runner created guest state: %v", names)
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("runner rewrote ios configuration")
	}
}

func TestIOSLifecycleDispatch(t *testing.T) {
	t.Setenv("COVE_STATE_DIR", t.TempDir())
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	ios.ROM = "avpbooter.rom"
	for _, name := range []string{"hw.model", "machine.id", "aux.img", "sep.img", "disk.img", ios.ROM} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("prepared fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 2, MemoryGB: 4, IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	hooks, _ := stubAcquireRunLockHook(t)
	called := false
	hooks.RunIOSVM = func(rc vmrun.RunConfig, hc vmrun.HostConfig, _ *RunBundle, _ runMetricRecorder) error {
		called = true
		if rc.OS != vmrun.GuestIOS || hc.VMDir != dir {
			t.Fatalf("wrong ios dispatch: %+v %+v", rc, hc)
		}
		return nil
	}
	hooks.RunMacOSVM = runHook(func() error { t.Fatal("ios routed to macos"); return nil })
	cfg := RunConfig{VM: vmSelection{Directory: dir}, VMRun: vmrun.RunConfig{OS: vmrun.GuestMacOS, CPUCount: 2, MemoryGB: 4}, Hooks: hooks}
	if err := runVMWithConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("ios runner not called")
	}
}

func TestIOSLifecycleRejectsBeforeMutation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*RunConfig)
	}{
		{"alternate disk", func(c *RunConfig) { c.SystemDiskPathOverride = "other.img" }},
		{"ram disk", func(c *RunConfig) { c.SystemDiskAttachment = systemDiskAttachmentTemporaryRAM }},
		{"disposable", func(c *RunConfig) { c.Disposable = true }},
		{"rollback", func(c *RunConfig) { c.RollbackSnapshot = "snapshot" }},
		{"ephemeral", func(c *RunConfig) { c.Ephemeral = true }},
		{"windows", func(c *RunConfig) { c.Windows = true }},
		{"linux", func(c *RunConfig) { c.Linux = true }},
		{"recovery", func(c *RunConfig) { c.VMRun.RecoveryMode = true }},
		{"gui", func(c *RunConfig) { c.VMRun.GUI = true }},
		{"skip resume", func(c *RunConfig) { c.VMRun.SkipResume = true }},
		{"save", func(c *RunConfig) { c.VMRun.SaveCompress = true }},
		{"identity", func(c *RunConfig) { c.VMHost.VMDir = c.VM.Directory; c.VMHost.RecoverIdentity = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ios := iosbundle.DefaultConfig()
			if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 2, MemoryGB: 4, IOS: &ios}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			cfg := RunConfig{VM: vmSelection{Directory: dir}, VMRun: vmrun.RunConfig{OS: vmrun.GuestMacOS, CPUCount: 2, MemoryGB: 4}, Hooks: RunHooks{
				ConsumeRunBudget: func(string, int) (int, error) { t.Fatal("consumed budget"); return 0, nil },
				AcquireRunLock:   func(string) (*RunLock, error) { t.Fatal("acquired lock"); return nil, nil },
			}}
			tt.change(&cfg)
			if err := runVMWithConfig(cfg); err == nil {
				t.Fatal("accepted unsupported ios lifecycle")
			}
			after, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("rewrote configuration")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "config.json" && !strings.HasPrefix(entry.Name(), ".") {
					t.Fatalf("created %s", entry.Name())
				}
			}
		})
	}
}

func TestIOSRunnerPreservesIncompleteState(t *testing.T) {
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	ios.ROM = "avpbooter.rom"
	if err := vmconfig.Save(dir, &vmconfig.Config{IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(disk, []byte("existing disk"), 0600); err != nil {
		t.Fatal(err)
	}
	err := runIOSVMWithConfig(vmrun.RunConfig{OS: vmrun.GuestIOS, CPUCount: 2, MemoryGB: 4}, vmrun.HostConfig{VMDir: dir}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "hw.model") {
		t.Fatalf("run = %v", err)
	}
	data, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing disk" {
		t.Fatal("overwrote disk")
	}
	for _, name := range []string{"hw.model", "machine.id", "aux.img", "sep.img"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("created %s: %v", name, err)
		}
	}
}

func TestIOSStopConfirmsState(t *testing.T) {
	for _, tt := range []struct {
		name    string
		state   vz.VZVirtualMachineState
		stopErr error
		wantErr bool
	}{
		{"stopped with callback error", vz.VZVirtualMachineStateStopped, errors.New("framework callback"), false},
		{"still running without callback error", vz.VZVirtualMachineStateRunning, nil, true},
		{"still running with callback error", vz.VZVirtualMachineStateRunning, errors.New("stop failed"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stopped := false
			err := stopIOSVM(func() <-chan error {
				stopped = true
				result := make(chan error, 1)
				result <- tt.stopErr
				return result
			}, func() (vz.VZVirtualMachineState, error) {
				if !stopped {
					return vz.VZVirtualMachineStateRunning, nil
				}
				return tt.state, nil
			}, 0, func() {})
			if (err != nil) != tt.wantErr {
				t.Fatalf("stop = %v", err)
			}
		})
	}
}

func TestIOSRuntimeDefaults(t *testing.T) {
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	if err := vmconfig.Save(dir, &vmconfig.Config{IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	original := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = original })
	for _, explicit := range []bool{false, true} {
		fs := flag.NewFlagSet("ios", flag.ContinueOnError)
		fs.Bool("rosetta", true, "")
		fs.Bool("clipboard", true, "")
		if explicit {
			if err := fs.Parse([]string{"-rosetta=true", "-clipboard=true"}); err != nil {
				t.Fatal(err)
			}
		}
		flag.CommandLine = fs
		cfg := RunConfig{VM: vmSelection{Directory: dir}, VMRun: vmrun.RunConfig{EnableRosetta: true, EnableClipboard: true}}
		applyIOSRuntimeDefaults(&cfg)
		if cfg.VMRun.EnableRosetta != explicit || cfg.VMRun.EnableClipboard != explicit {
			t.Fatalf("explicit %v: %+v", explicit, cfg.VMRun)
		}
	}
}
