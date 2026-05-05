package softreset

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ListProcesses(context.Context) ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := parsePID(entry.Name())
		if err != nil {
			continue
		}
		p, err := readLinuxProcess(pid)
		if err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func readLinuxProcess(pid int) (Process, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
	cmd := strings.TrimSpace(string(bytes.ReplaceAll(bytes.Trim(cmdline, "\x00"), []byte{0}, []byte{' '})))
	if cmd == "" {
		comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
		cmd = strings.TrimSpace(string(comm))
	}
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return Process{}, err
	}
	uid := -1
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				uid, err = parseUID(fields[0])
				if err != nil {
					return Process{}, err
				}
			}
			break
		}
	}
	if uid < 0 {
		return Process{}, os.ErrInvalid
	}
	return Process{PID: pid, UID: uid, Cmdline: cmd}, nil
}
