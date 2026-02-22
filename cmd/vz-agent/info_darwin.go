package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// populateSystemInfo fills in OS-specific fields via sysctl.
func populateSystemInfo(info *systemInfo) {
	if out, err := exec.Command("sysctl", "-n", "kern.osproductversion").Output(); err == nil {
		info.OSVersion = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("sysctl", "-n", "kern.osversion").Output(); err == nil {
		info.KernelVersion = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &info.MemoryTotal)
	}
	// Available memory: free + inactive pages (reclaimable by OS).
	var pageSize, freePages, inactivePages uint64
	if out, err := exec.Command("sysctl", "-n", "hw.pagesize").Output(); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pageSize)
	}
	if out, err := exec.Command("sysctl", "-n", "vm.page_free_count").Output(); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &freePages)
	}
	if out, err := exec.Command("sysctl", "-n", "vm.page_inactive_count").Output(); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &inactivePages)
	}
	if pageSize > 0 {
		info.MemoryAvailable = (freePages + inactivePages) * pageSize
	}
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		fmt.Sscanf(strings.TrimSpace(string(out)), "{ %f %f %f }",
			&info.LoadAvg1, &info.LoadAvg5, &info.LoadAvg15)
	}
	if out, err := exec.Command("sysctl", "-n", "kern.boottime").Output(); err == nil {
		var sec int64
		fmt.Sscanf(string(out), "{ sec = %d", &sec)
		if sec > 0 {
			info.UptimeSeconds = uint64(time.Now().Unix() - sec)
		}
	}
}

// listLocalUsers returns non-system users via dscl.
func listLocalUsers() ([]string, error) {
	out, err := exec.Command("dscl", ".", "-list", "/Users").Output()
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
