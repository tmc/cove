package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type cpDirection int

const (
	cpHostToGuest cpDirection = iota
	cpGuestToHost
)

type cpSpec struct {
	Direction cpDirection
	VM        string
	HostPath  string
	GuestPath string
}

type cpAgent interface {
	CopyToGuest(ctx context.Context, hostPath, guestPath string) error
	CopyFromGuest(ctx context.Context, guestPath, hostPath string) error
}

type controlCpAgent struct {
	client *ControlClient
}

func handleCpCommand(args []string) error {
	return runCp(context.Background(), args, newControlCpAgent)
}

func runCp(ctx context.Context, args []string, newAgent func(string) cpAgent) error {
	fs := flag.NewFlagSet("cp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove cp <host-path> <vm:/guest/path> | <vm:/guest/path> <host-path>")
	}
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: cove cp <host-path> <vm:/guest/path> | <vm:/guest/path> <host-path>")
	}
	spec, err := parseCpSpec(fs.Arg(0), fs.Arg(1))
	if err != nil {
		return err
	}
	agent := newAgent(spec.VM)
	switch spec.Direction {
	case cpHostToGuest:
		return agent.CopyToGuest(ctx, spec.HostPath, spec.GuestPath)
	case cpGuestToHost:
		return agent.CopyFromGuest(ctx, spec.GuestPath, spec.HostPath)
	default:
		return errors.New("cp: invalid direction")
	}
}

func parseCpSpec(src, dst string) (cpSpec, error) {
	srcRemote, srcVM, srcPath, err := parseCpOperand(src)
	if err != nil {
		return cpSpec{}, err
	}
	dstRemote, dstVM, dstPath, err := parseCpOperand(dst)
	if err != nil {
		return cpSpec{}, err
	}
	if srcRemote == dstRemote {
		return cpSpec{}, errors.New("cp: exactly one path must be remote in the form vm:/absolute/guest/path")
	}
	if srcRemote {
		hostPath, err := cleanHostPath(dstPath)
		if err != nil {
			return cpSpec{}, err
		}
		return cpSpec{
			Direction: cpGuestToHost,
			VM:        srcVM,
			GuestPath: srcPath,
			HostPath:  hostPath,
		}, nil
	}
	hostPath, err := cleanHostPath(srcPath)
	if err != nil {
		return cpSpec{}, err
	}
	return cpSpec{
		Direction: cpHostToGuest,
		VM:        dstVM,
		HostPath:  hostPath,
		GuestPath: dstPath,
	}, nil
}

func parseCpOperand(s string) (remote bool, vm, path string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return false, "", "", errors.New("cp: empty path")
	}
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return false, "", s, nil
	}
	vm, path = s[:i], s[i+1:]
	if vm == "" || path == "" {
		return false, "", "", fmt.Errorf("cp: invalid remote path %q; want vm:/absolute/guest/path", s)
	}
	if strings.Contains(vm, "/") {
		return false, "", "", fmt.Errorf("cp: invalid VM name %q in remote path", vm)
	}
	if !strings.HasPrefix(path, "/") {
		return false, "", "", fmt.Errorf("cp: guest path must be absolute in %q", s)
	}
	return true, vm, path, nil
}

func cleanHostPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("cp: empty host path")
	}
	if strings.Contains(path, ":") {
		return "", fmt.Errorf("cp: host path %q contains ':'; exactly one operand must be vm:/absolute/guest/path", path)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cp: resolve host path: %w", err)
	}
	return filepath.Join(wd, path), nil
}

func newControlCpAgent(vm string) cpAgent {
	socketPath := filepath.Join(vmconfig.BaseDir(), vm, "control.sock")
	client := NewControlClient(socketPath)
	client.SetTimeout(10 * time.Minute)
	return controlCpAgent{client: client}
}

func (a controlCpAgent) CopyToGuest(_ context.Context, hostPath, guestPath string) error {
	req := &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{AgentCp: &controlpb.AgentCopyCommand{
			HostPath:  hostPath,
			GuestPath: guestPath,
			ToGuest:   true,
		}},
	}
	resp, err := a.client.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("cp: %s", resp.Error)
	}
	return nil
}

func (a controlCpAgent) CopyFromGuest(_ context.Context, guestPath, hostPath string) error {
	req := &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{AgentCp: &controlpb.AgentCopyCommand{
			HostPath:  hostPath,
			GuestPath: guestPath,
			ToGuest:   false,
		}},
	}
	resp, err := a.client.sendRequest(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("cp: %s", resp.Error)
	}
	return nil
}
