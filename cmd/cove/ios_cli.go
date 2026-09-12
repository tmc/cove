package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
)

func runIOSCommand(env commandEnv, _ string, args []string) int {
	env = env.WithDefaultIO()
	if len(args) == 0 || isHelpArg(args[0]) {
		fmt.Fprintln(env.Stdout, "Usage: cove ios validate -vm-dir PATH\n       cove ios probe\n\nvalidate checks prepared files without writing or starting a VM\nprobe checks native research APIs without creating a VM")
		return 0
	}
	switch args[0] {
	case "probe":
		if len(args) != 1 {
			return commandUsageError(env, fmt.Errorf("ios probe takes no arguments"))
		}
		return commandError(env, probeIOSHost(env.Stdout))
	case "validate":
		fs := flag.NewFlagSet("ios validate", flag.ContinueOnError)
		fs.SetOutput(env.Stderr)
		dir := fs.String("vm-dir", "", "prepared bundle directory")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if *dir == "" || fs.NArg() != 0 {
			return commandUsageError(env, fmt.Errorf("usage: cove ios validate -vm-dir PATH"))
		}
		path, err := filepath.Abs(*dir)
		if err != nil {
			return commandError(env, err)
		}
		if err := validateIOSPreparedBundle(path); err != nil {
			return commandError(env, err)
		}
		fmt.Fprintln(env.Stdout, "Prepared iOS files validated; native start, DFU discovery and guest boot are unverified.")
		return 0
	default:
		return commandUsageError(env, fmt.Errorf("unknown ios command %q", args[0]))
	}
}

func validateIOSPreparedBundle(dir string) error {
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		return fmt.Errorf("ios config.json: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("ios config.json must be a nonempty regular file")
	}
	saved, err := vmconfig.Load(dir)
	if err != nil {
		return err
	}
	if saved.IOS == nil {
		return fmt.Errorf("ios validation requires an ios configuration")
	}
	var errs []error
	if err := saved.IOS.ValidatePrepared(dir); err != nil {
		errs = append(errs, err)
	}
	cpu, memory := saved.CPU, saved.MemoryGB
	if cpu == 0 {
		cpu = 1
	}
	if memory == 0 {
		memory = 1
	}
	rc := vmrun.RunConfig{OS: vmrun.GuestIOS, CPUCount: cpu, MemoryGB: memory}
	if len(saved.Volumes) != 0 {
		errs = append(errs, fmt.Errorf("ios does not support saved shared folders; remove volumes from config.json"))
	}
	if err := validateIOSRun(rc, vmrun.HostConfig{VMDir: dir}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
