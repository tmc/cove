package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	fs := flag.NewFlagSet("forward", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return forwardSpec{}, err
	}
	if fs.NArg() != 2 {
		return forwardSpec{}, errors.New("usage: cove forward <vm> <hostport>:<vmport>")
	}
	return parseForwardSpec(fs.Arg(0), fs.Arg(1))
}

func parseForwardSpec(vm, mapping string) (forwardSpec, error) {
	vm = strings.TrimSpace(vm)
	if vm == "" {
		return forwardSpec{}, errors.New("forward: vm required")
	}
	if strings.Contains(vm, "/") {
		return forwardSpec{}, fmt.Errorf("forward: invalid VM name %q", vm)
	}
	host, guest, err := parseForwardPorts(mapping)
	if err != nil {
		return forwardSpec{}, err
	}
	relay := uint32(forwardRelayBasePort + host%forwardRelayPortWindow)
	return forwardSpec{
		VM:        vm,
		HostPort:  host,
		GuestPort: guest,
		RelayPort: relay,
		AgentPort: relay + forwardAgentPortOffset,
	}, nil
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
