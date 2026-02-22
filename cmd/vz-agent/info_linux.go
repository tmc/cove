package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// populateSystemInfo fills in OS-specific fields from /proc and /etc.
func populateSystemInfo(info *systemInfo) {
	// OS version from /etc/os-release
	if f, err := os.Open("/etc/os-release"); err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := s.Text()
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OSVersion = strings.Trim(line[len("PRETTY_NAME="):], "\"")
			}
		}
	}

	// Kernel version from /proc/version
	if data, err := os.ReadFile("/proc/version"); err == nil {
		parts := strings.Fields(string(data))
		if len(parts) >= 3 {
			info.KernelVersion = parts[2]
		}
	}

	// Memory from /proc/meminfo
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := s.Text()
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
						info.MemoryTotal = kb * 1024
					}
				}
			}
			if strings.HasPrefix(line, "MemAvailable:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
						info.MemoryAvailable = kb * 1024
					}
				}
			}
		}
	}

	// Load averages from /proc/loadavg
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fmt.Sscanf(string(data), "%f %f %f", &info.LoadAvg1, &info.LoadAvg5, &info.LoadAvg15)
	}

	// Uptime from /proc/uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		var up float64
		fmt.Sscanf(string(data), "%f", &up)
		info.UptimeSeconds = uint64(up)
	}
}

// listLocalUsers returns non-system users from /etc/passwd.
func listLocalUsers() ([]string, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var users []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.SplitN(s.Text(), ":", 4)
		if len(fields) < 4 {
			continue
		}
		name := fields[0]
		uid, _ := strconv.Atoi(fields[2])
		// Skip system users (uid < 1000) and nobody (65534)
		if uid < 1000 || uid == 65534 {
			continue
		}
		users = append(users, name)
	}
	return users, nil
}
