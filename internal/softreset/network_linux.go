package softreset

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
)

func ListNetworkSockets(ctx context.Context) ([]NetworkSocket, error) {
	cmd := exec.CommandContext(ctx, "ss", "-tunap")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var sockets []NetworkSocket
	scan := bufio.NewScanner(strings.NewReader(string(out)))
	for scan.Scan() {
		s, ok := parseSSNetworkLine(scan.Text())
		if ok {
			sockets = append(sockets, s)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return sockets, nil
}

func parseSSNetworkLine(line string) (NetworkSocket, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] == "Netid" {
		return NetworkSocket{}, false
	}
	protocol := strings.ToLower(fields[0])
	state := ""
	localIndex := 3
	if protocol == "tcp" {
		state = fields[1]
		localIndex = 4
	}
	if len(fields) <= localIndex+1 {
		return NetworkSocket{}, false
	}
	process := ""
	if len(fields) > localIndex+2 {
		process = strings.Join(fields[localIndex+2:], " ")
	}
	return NetworkSocket{
		Protocol: protocol,
		Local:    fields[localIndex],
		Remote:   fields[localIndex+1],
		State:    state,
		Process:  process,
	}, true
}
