package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// populateSystemInfo fills in OS-specific fields via sysctl.
func populateSystemInfo(ctx context.Context, info *systemInfo) {
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "kern.osproductversion"); err == nil {
		info.OSVersion = strings.TrimSpace(string(out))
	}
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "kern.osversion"); err == nil {
		info.KernelVersion = strings.TrimSpace(string(out))
	}
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "hw.memsize"); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &info.MemoryTotal)
	}
	// Available memory: free + inactive pages (reclaimable by OS).
	var pageSize, freePages, inactivePages uint64
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "hw.pagesize"); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pageSize)
	}
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "vm.page_free_count"); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &freePages)
	}
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "vm.page_inactive_count"); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &inactivePages)
	}
	if pageSize > 0 {
		info.MemoryAvailable = (freePages + inactivePages) * pageSize
	}
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "vm.loadavg"); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "{ %f %f %f }",
			&info.LoadAvg1, &info.LoadAvg5, &info.LoadAvg15)
	}
	if out, err := commandOutput(ctx, time.Second, "sysctl", "-n", "kern.boottime"); err == nil {
		var sec int64
		fmt.Sscanf(string(out), "{ sec = %d", &sec)
		if sec > 0 {
			info.UptimeSeconds = uint64(time.Now().Unix() - sec)
		}
	}
}

// listLocalUsers returns non-system users via dscl.
func listLocalUsers(ctx context.Context) ([]string, error) {
	out, err := commandOutput(ctx, 2*time.Second, "dscl", ".", "-list", "/Users")
	if err != nil {
		return nil, err
	}
	var users []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "_") {
			continue
		}
		users = append(users, name)
	}
	return users, nil
}

func commandOutput(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}
