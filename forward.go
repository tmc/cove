package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const (
	forwardRelayBasePort   = 20000
	forwardRelayPortWindow = 20000
	forwardAgentPortOffset = 20000
)

type forwardSpec struct {
	VM        string
	Reverse   bool
	HostPort  int
	GuestPort int
	RelayPort uint32
	AgentPort uint32
}

type forwardStarter interface {
	StartForward(ctx context.Context, spec forwardSpec) (string, error)
}

type controlForwardStarter struct {
	client *ControlClient
}

func forwardCommand(args []string) error {
	return runForward(context.Background(), args, newControlForwardStarter)
}

func runForward(ctx context.Context, args []string, newStarter func(string) forwardStarter) error {
	spec, err := parseForwardArgs(args)
	if err != nil {
		return err
	}
	msg, err := newStarter(spec.VM).StartForward(ctx, spec)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func parseForwardArgs(args []string) (forwardSpec, error) {
	if len(args) == 3 && (args[1] == "-reverse" || args[1] == "--reverse") {
		return parseReverseForwardSpec(args[0], args[2])
	}
	if len(args) != 2 {
		return forwardSpec{}, errors.New("usage: cove forward <vm> <hostport>:<vmport>")
	}
	if strings.Contains(args[1], "->") {
		return parseNaturalForwardSpec(args[0], args[1])
	}
	return parseForwardSpec(args[0], args[1])
}

func parseForwardSpec(vm, mapping string) (forwardSpec, error) {
	return parseForwardSpecWithDirection(vm, mapping, false)
}

func parseReverseForwardSpec(vm, mapping string) (forwardSpec, error) {
	return parseForwardSpecWithDirection(vm, mapping, true)
}

func parseForwardSpecWithDirection(vm, mapping string, reverse bool) (forwardSpec, error) {
	vm = strings.TrimSpace(vm)
	if vm == "" {
		return forwardSpec{}, errors.New("forward: vm required")
	}
	if strings.Contains(vm, "/") {
		return forwardSpec{}, fmt.Errorf("forward: invalid VM name %q", vm)
	}
	first, second, err := parseForwardPorts(mapping)
	if err != nil {
		return forwardSpec{}, err
	}
	host, guest := first, second
	if reverse {
		host, guest = second, first
	}
	relayKey := host
	if reverse {
		relayKey = guest
	}
	relay := uint32(forwardRelayBasePort + relayKey%forwardRelayPortWindow)
	return forwardSpec{
		VM:        vm,
		Reverse:   reverse,
		HostPort:  host,
		GuestPort: guest,
		RelayPort: relay,
		AgentPort: relay + forwardAgentPortOffset,
	}, nil
}

func parseNaturalForwardSpec(vm, mapping string) (forwardSpec, error) {
	parts := strings.SplitN(strings.TrimSpace(mapping), "->", 2)
	if len(parts) != 2 {
		return forwardSpec{}, fmt.Errorf("forward: expected vm:port->host:port, got %q", mapping)
	}
	leftName, leftPort, err := parseForwardEndpoint(parts[0])
	if err != nil {
		return forwardSpec{}, err
	}
	rightName, rightPort, err := parseForwardEndpoint(parts[1])
	if err != nil {
		return forwardSpec{}, err
	}
	switch {
	case leftName == "host" && rightName == "vm":
		return parseForwardSpec(vm, leftPort+":"+rightPort)
	case leftName == "vm" && rightName == "host":
		return parseReverseForwardSpec(vm, leftPort+":"+rightPort)
	default:
		return forwardSpec{}, fmt.Errorf("forward: expected host:port->vm:port or vm:port->host:port, got %q", mapping)
	}
}

func parseForwardEndpoint(endpoint string) (string, string, error) {
	name, port, ok := strings.Cut(strings.TrimSpace(endpoint), ":")
	if !ok || name == "" || port == "" || strings.Contains(port, ":") {
		return "", "", fmt.Errorf("forward: invalid endpoint %q", endpoint)
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "host" && name != "vm" {
		return "", "", fmt.Errorf("forward: invalid endpoint %q", endpoint)
	}
	return name, port, nil
}

func parseForwardPorts(mapping string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(mapping), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(parts[1], ":") {
		return 0, 0, fmt.Errorf("forward: expected hostport:vmport, got %q", mapping)
	}
	host, err := parseForwardPort(parts[0], "host")
	if err != nil {
		return 0, 0, err
	}
	guest, err := parseForwardPort(parts[1], "vm")
	if err != nil {
		return 0, 0, err
	}
	return host, guest, nil
}

func parseForwardPort(s, name string) (int, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("forward: invalid %s port %q", name, s)
	}
	return int(n), nil
}

func newControlForwardStarter(vm string) forwardStarter {
	sock := GetControlSocketPathForVM(filepath.Join(vmconfig.BaseDir(), vm))
	client := NewControlClient(sock)
	client.SetTimeout(10 * time.Minute)
	return controlForwardStarter{client: client}
}

func (s controlForwardStarter) StartForward(ctx context.Context, spec forwardSpec) (string, error) {
	if spec.Reverse {
		return s.startReverseForward(ctx, spec)
	}
	if err := s.startGuestRelay(ctx, spec); err != nil {
		return "", err
	}
	req := &controlpb.ControlRequest{
		Type: "port-forward",
		Command: &controlpb.ControlRequest_PortForward{PortForward: &controlpb.PortForwardCommand{
			Action:    "start",
			HostPort:  uint32(spec.HostPort),
			GuestPort: spec.RelayPort,
		}},
	}
	resp, err := s.client.sendRequest(req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("forward: %s", resp.Error)
	}
	return fmt.Sprintf("forwarding localhost:%d -> %s:127.0.0.1:%d", spec.HostPort, spec.VM, spec.GuestPort), nil
}

func (s controlForwardStarter) startReverseForward(ctx context.Context, spec forwardSpec) (string, error) {
	req := &controlpb.ControlRequest{
		Type: "port-forward",
		Command: &controlpb.ControlRequest_PortForward{PortForward: &controlpb.PortForwardCommand{
			Action:    "start-reverse",
			HostPort:  uint32(spec.HostPort),
			GuestPort: uint32(spec.GuestPort),
		}},
	}
	resp, err := s.client.sendRequest(req)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("forward: %s", resp.Error)
	}
	if err := s.startGuestReverseRelay(ctx, spec); err != nil {
		return "", err
	}
	return fmt.Sprintf("forwarding %s:127.0.0.1:%d -> localhost:%d", spec.VM, spec.GuestPort, spec.HostPort), nil
}

func (s controlForwardStarter) startGuestRelay(ctx context.Context, spec forwardSpec) error {
	logPath := fmt.Sprintf("/tmp/cove-forward-%d-%d.log", spec.HostPort, spec.GuestPort)
	relaySpec := fmt.Sprintf("%d:127.0.0.1:%d", spec.RelayPort, spec.GuestPort)
	script := fmt.Sprintf("nohup /usr/local/bin/vz-agent -port %d -relay %s >%s 2>&1 &", spec.AgentPort, shellQuote(relaySpec), shellQuote(logPath))
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{
			Args: []string{"/bin/sh", "-c", script},
		}},
	}
	resp, err := s.client.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("forward: start guest relay: %s", resp.Error)
	}
	if result := resp.GetAgentExecResult(); result != nil && result.GetExitCode() != 0 {
		return fmt.Errorf("forward: start guest relay exited %d: %s", result.GetExitCode(), strings.TrimSpace(result.GetStderr()))
	}
	return nil
}

func (s controlForwardStarter) startGuestReverseRelay(ctx context.Context, spec forwardSpec) error {
	logPath := fmt.Sprintf("/tmp/cove-forward-reverse-%d-%d.log", spec.GuestPort, spec.HostPort)
	relaySpec := fmt.Sprintf("%d:%d", spec.GuestPort, spec.RelayPort)
	script := fmt.Sprintf("nohup /usr/local/bin/vz-agent -port %d -reverse-relay %s >%s 2>&1 &", spec.AgentPort, shellQuote(relaySpec), shellQuote(logPath))
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{
			Args: []string{"/bin/sh", "-c", script},
		}},
	}
	resp, err := s.client.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("forward: start guest reverse relay: %s", resp.Error)
	}
	if result := resp.GetAgentExecResult(); result != nil && result.GetExitCode() != 0 {
		return fmt.Errorf("forward: start guest reverse relay exited %d: %s", result.GetExitCode(), strings.TrimSpace(result.GetStderr()))
	}
	return nil
}
