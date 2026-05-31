//go:build darwin || linux

package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const topProcessInfoLimit = 5

var systemProcessOutput = func(ctx context.Context) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return exec.CommandContext(cmdCtx, "ps", "-axo", "pid=,pcpu=,rss=,comm=").Output()
}

func populateProcessInfo(ctx context.Context, info *systemInfo) {
	if info == nil {
		return
	}
	out, err := systemProcessOutput(ctx)
	if err != nil {
		return
	}
	count, top := parseSystemProcessPS(out, topProcessInfoLimit)
	info.ProcessCount = uint32(count)
	info.TopProcesses = top
}

func parseSystemProcessPS(out []byte, limit int) (int, []systemProcessInfo) {
	var procs []systemProcessInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil || pid <= 0 {
			continue
		}
		cpu, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || cpu < 0 {
			continue
		}
		rssKB, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			continue
		}
		procs = append(procs, systemProcessInfo{
			PID:        int32(pid),
			CPUPercent: cpu,
			RSSBytes:   rssKB * 1024,
			Command:    processCommandLabel(strings.Join(fields[3:], " ")),
		})
	}
	sort.Slice(procs, func(i, j int) bool {
		if procs[i].CPUPercent != procs[j].CPUPercent {
			return procs[i].CPUPercent > procs[j].CPUPercent
		}
		if procs[i].RSSBytes != procs[j].RSSBytes {
			return procs[i].RSSBytes > procs[j].RSSBytes
		}
		return procs[i].PID < procs[j].PID
	})
	count := len(procs)
	if limit <= 0 || len(procs) <= limit {
		return count, procs
	}
	return count, procs[:limit]
}

func processCommandLabel(command string) string {
	command = strings.TrimSpace(command)
	command = strings.Trim(command, "()")
	if command == "" {
		return ""
	}
	base := filepath.Base(command)
	base = strings.Trim(base, "()")
	if base == "" || base == "." || base == string(filepath.Separator) {
		return command
	}
	return base
}
