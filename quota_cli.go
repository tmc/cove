package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmquota"
)

type quotaCommand struct {
	VM     string
	Action string
	Value  uint64
}

type quotaManager interface {
	Show(ctx context.Context, vm string) (vmquota.Quota, error)
	SetCPU(ctx context.Context, vm string, cpus uint) error
	SetMemory(ctx context.Context, vm string, gb uint64) error
	SetDisk(ctx context.Context, vm string, gb uint64) error
}

type fileQuotaManager struct{}

func handleQuotaCommand(args []string) error {
	return runQuota(context.Background(), args, fileQuotaManager{}, os.Stdout)
}

func runQuota(ctx context.Context, args []string, manager quotaManager, out io.Writer) error {
	cmd, err := parseQuotaArgs(args)
	if errors.Is(err, errFlagHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	switch cmd.Action {
	case "show":
		q, err := manager.Show(ctx, cmd.VM)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "vm: %s\ncpu: %d\nmemory: %d GB\ndisk: %d GB\n", cmd.VM, q.CPUs, q.MemoryGB, q.DiskGB)
	case "cpu":
		return manager.SetCPU(ctx, cmd.VM, uint(cmd.Value))
	case "memory":
		return manager.SetMemory(ctx, cmd.VM, cmd.Value)
	case "disk":
		return manager.SetDisk(ctx, cmd.VM, cmd.Value)
	default:
		return fmt.Errorf("quota: unknown action %q", cmd.Action)
	}
	return nil
}

func parseQuotaArgs(args []string) (quotaCommand, error) {
	fs := flag.NewFlagSet("quota", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { printQuotaUsage(os.Stdout) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		return quotaCommand{}, err
	}
	if fs.NArg() < 2 {
		return quotaCommand{}, errors.New("usage: cove quota <vm> show | cpu <n> | memory <gb> | disk <gb>")
	}
	vm := strings.TrimSpace(fs.Arg(0))
	if vm == "" {
		return quotaCommand{}, errors.New("quota: vm required")
	}
	if strings.Contains(vm, "/") {
		return quotaCommand{}, fmt.Errorf("quota: invalid VM name %q", vm)
	}
	action := strings.ToLower(fs.Arg(1))
	switch action {
	case "show":
		if fs.NArg() != 2 {
			return quotaCommand{}, errors.New("usage: cove quota <vm> show")
		}
		return quotaCommand{VM: vm, Action: action}, nil
	case "cpu", "memory", "disk":
		if fs.NArg() != 3 {
			return quotaCommand{}, fmt.Errorf("usage: cove quota <vm> %s <n>", action)
		}
		value, err := parseQuotaValue(fs.Arg(2), action)
		if err != nil {
			return quotaCommand{}, err
		}
		return quotaCommand{VM: vm, Action: action, Value: value}, nil
	default:
		return quotaCommand{}, fmt.Errorf("quota: unknown action %q", action)
	}
}

func printQuotaUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove quota <vm> show
       cove quota <vm> cpu <n>
       cove quota <vm> memory <gb>
       cove quota <vm> disk <gb>

Show or update saved resource quotas for a VM. Memory and disk values are in
GiB; CPU is a whole vCPU count.`)
}

func parseQuotaValue(s, name string) (uint64, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("quota: invalid %s value %q", name, s)
	}
	return n, nil
}

func (fileQuotaManager) Show(ctx context.Context, vm string) (vmquota.Quota, error) {
	return loadQuotaForVM(vm)
}

func (fileQuotaManager) SetCPU(ctx context.Context, vm string, cpus uint) error {
	dir, q, err := loadQuotaPath(vm)
	if err != nil {
		return err
	}
	q.CPUs = cpus
	if err := vmquota.Save(dir, q); err != nil {
		return err
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		return err
	}
	cfg.CPU = cpus
	return vmconfig.Save(dir, cfg)
}

func (fileQuotaManager) SetMemory(ctx context.Context, vm string, gb uint64) error {
	dir, q, err := loadQuotaPath(vm)
	if err != nil {
		return err
	}
	q.MemoryGB = gb
	if err := vmquota.Save(dir, q); err != nil {
		return err
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		return err
	}
	cfg.MemoryGB = gb
	return vmconfig.Save(dir, cfg)
}

func (fileQuotaManager) SetDisk(ctx context.Context, vm string, gb uint64) error {
	dir, q, err := loadQuotaPath(vm)
	if err != nil {
		return err
	}
	q.DiskGB = gb
	if err := vmquota.ApplyAPFSQuota(dir, gb); err != nil {
		return err
	}
	if err := vmquota.Save(dir, q); err != nil {
		return err
	}
	return nil
}

func loadQuotaForVM(vm string) (vmquota.Quota, error) {
	_, q, err := loadQuotaPath(vm)
	return q, err
}

func loadQuotaPath(vm string) (string, vmquota.Quota, error) {
	dir, ok := vmconfig.ExistingPath(vm)
	if !ok {
		return "", vmquota.Quota{}, fmt.Errorf("quota: no VM named %q under %s", vm, vmconfig.BaseDir())
	}
	q, err := vmquota.Load(dir)
	if err != nil {
		return "", vmquota.Quota{}, err
	}
	return dir, q, nil
}

func persistInstallQuota(dir string) {
	q := vmquota.Quota{CPUs: cpuCount, MemoryGB: memoryGB, DiskGB: diskSizeGB}
	if err := vmquota.Save(dir, q); err != nil {
		fmt.Printf("warning: save quota config: %v\n", err)
	}
}

func applyInstallDiskQuota(dir string) error {
	if diskSizeGB == 0 {
		return nil
	}
	if err := applyAPFSQuotaForInstall(dir, diskSizeGB); err != nil {
		if errors.Is(err, vmquota.ErrAPFSQuotaUnsupported) ||
			strings.Contains(err.Error(), `did not recognize APFS verb "setQuota"`) {
			fmt.Printf("warning: APFS directory quotas are not supported on this host; continuing without host disk quota: %v\n", err)
			return nil
		}
		return err
	}
	return nil
}

var applyAPFSQuotaForInstall = vmquota.ApplyAPFSQuota
