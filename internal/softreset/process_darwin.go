package softreset

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
)

func ListProcesses(ctx context.Context) ([]Process, error) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,uid=,command=")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var processes []Process
	scan := bufio.NewScanner(strings.NewReader(string(out)))
	for scan.Scan() {
		p, ok := parsePSLine(scan.Text())
		if ok {
			processes = append(processes, p)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return processes, nil
}

func parsePSLine(line string) (Process, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return Process{}, false
	}
	pid, err := parsePID(fields[0])
	if err != nil {
		return Process{}, false
	}
	uid, err := parseUID(fields[1])
	if err != nil {
		return Process{}, false
	}
	return Process{PID: pid, UID: uid, Cmdline: strings.Join(fields[2:], " ")}, true
}
