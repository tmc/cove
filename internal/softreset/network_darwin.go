package softreset

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
)

func ListNetworkSockets(ctx context.Context) ([]NetworkSocket, error) {
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-iUDP")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var sockets []NetworkSocket
	scan := bufio.NewScanner(strings.NewReader(string(out)))
	for scan.Scan() {
		s, ok := parseLsofNetworkLine(scan.Text())
		if ok {
			sockets = append(sockets, s)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return sockets, nil
}

func parseLsofNetworkLine(line string) (NetworkSocket, bool) {
	fields := strings.Fields(line)
	if len(fields) < 9 || fields[0] == "COMMAND" {
		return NetworkSocket{}, false
	}
	name := strings.Join(fields[8:], " ")
	local, remote, _ := strings.Cut(name, "->")
	state := ""
	if i := strings.LastIndex(name, "("); i >= 0 && strings.HasSuffix(name, ")") {
		state = strings.TrimSuffix(name[i+1:], ")")
	}
	protocol := strings.ToLower(fields[7])
	if strings.Contains(protocol, "tcp") {
		protocol = "tcp"
	} else if strings.Contains(protocol, "udp") {
		protocol = "udp"
	}
	return NetworkSocket{
		Protocol: protocol,
		Local:    strings.TrimSpace(local),
		Remote:   strings.TrimSpace(remote),
		State:    state,
		Process:  fields[0],
	}, true
}
