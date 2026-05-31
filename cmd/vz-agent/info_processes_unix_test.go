//go:build darwin || linux

package main

import (
	"context"
	"testing"
)

func TestParseSystemProcessPSSortsAndBounds(t *testing.T) {
	count, top := parseSystemProcessPS([]byte(`
101 0.0 100 /sbin/launchd
102 42.5 2048 /usr/bin/python3
103 42.5 4096 /opt/tool/build-worker
104 1.5 512 (kernel_task)
bad line
105 nope 10 broken
`), 3)
	if count != 4 {
		t.Fatalf("count = %d, want 4", count)
	}
	if len(top) != 3 {
		t.Fatalf("top len = %d, want 3", len(top))
	}
	if top[0].PID != 103 || top[0].CPUPercent != 42.5 || top[0].RSSBytes != 4096*1024 || top[0].Command != "build-worker" {
		t.Fatalf("top[0] = %+v", top[0])
	}
	if top[1].PID != 102 || top[1].Command != "python3" {
		t.Fatalf("top[1] = %+v", top[1])
	}
	if top[2].PID != 104 || top[2].Command != "kernel_task" {
		t.Fatalf("top[2] = %+v", top[2])
	}
}

func TestPopulateProcessInfoUsesProcessOutput(t *testing.T) {
	prev := systemProcessOutput
	systemProcessOutput = func(context.Context) ([]byte, error) {
		return []byte("201 2.0 1000 /bin/zsh\n202 7.5 2000 /usr/bin/make\n"), nil
	}
	t.Cleanup(func() { systemProcessOutput = prev })

	var info systemInfo
	populateProcessInfo(context.Background(), &info)
	if info.ProcessCount != 2 {
		t.Fatalf("ProcessCount = %d, want 2", info.ProcessCount)
	}
	if len(info.TopProcesses) != 2 || info.TopProcesses[0].Command != "make" || info.TopProcesses[1].Command != "zsh" {
		t.Fatalf("TopProcesses = %+v", info.TopProcesses)
	}
}
