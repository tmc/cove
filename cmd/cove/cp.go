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

	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
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
	vm     string
	route  controlpb.AgentRoute
}

func handleCpCommand(args []string) error {
	return runCp(context.Background(), args, newControlCpAgent)
}

func runCp(ctx context.Context, args []string, newAgent func(string) cpAgent) error {
	fs := flag.NewFlagSet("cp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vmFlag := fs.String("vm", "", "VM name; must match vm:/path endpoint if both are provided")
	daemonFlag := fs.Bool("daemon", false, "force the root daemon agent (vsock 1024)")
	userFlag := fs.Bool("user", false, "force the logged-in user agent (vsock 1025); use for TCC paths or to keep a large transfer off the daemon channel")
	fs.Usage = func() { printCpUsage(os.Stdout) }
	if err := parseFlagsOrHelp(fs, moveCpFlagsFirst(args)); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if *daemonFlag && *userFlag {
		return errors.New("cp: -daemon and -user are mutually exclusive")
	}
	if fs.NArg() != 2 {
		return errors.New("usage: cove cp [-vm name] [-daemon|-user] <host-path> <vm:/guest/path> | <vm:/guest/path> <host-path>")
	}
	selectedVM := *vmFlag
	if selectedVM == "" && flagWasSet("vm") {
		selectedVM = vmName
	}
	spec, err := parseCpSpecForVM(fs.Arg(0), fs.Arg(1), selectedVM)
	if err != nil {
		return err
	}
	agent := newAgent(spec.VM)
	if ca, ok := agent.(routableCpAgent); ok {
		switch {
		case *daemonFlag:
			ca.setRoute(controlpb.AgentRoute_AGENT_ROUTE_DAEMON)
		case *userFlag:
			ca.setRoute(controlpb.AgentRoute_AGENT_ROUTE_USER)
		}
	}
	switch spec.Direction {
	case cpHostToGuest:
		return agent.CopyToGuest(ctx, spec.HostPath, spec.GuestPath)
	case cpGuestToHost:
		return agent.CopyFromGuest(ctx, spec.GuestPath, spec.HostPath)
	default:
		return errors.New("cp: invalid direction")
	}
}

func printCpUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove cp [-vm name] [-daemon|-user] <host-path> <vm:/guest/path>
       cove cp [-vm name] [-daemon|-user] <vm:/guest/path> <host-path>

Copy files between the host and a running guest through the VM control socket.
The VM selected by -vm must match the vm:/path endpoint when both are present.
The -vm flag may appear before or after the copy operands.

By default the guest path picks the agent: TCC-protected paths (/Volumes/<tag>,
~/Downloads, ...) use the logged-in user agent, the rest use the root daemon.
Force one with:
  -daemon  root daemon agent (vsock 1024)
  -user    logged-in user agent (vsock 1025) — a separate vsock connection, so a
           large transfer does not share the daemon channel that cove shell uses

Examples:
  cove cp ./app.log work-vm:/tmp/app.log
  cove cp -vm work-vm work-vm:/tmp/app.log ./app.log
  cove cp -user ./model.bin work-vm:/Volumes/models/model.bin`)
}

func moveCpFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-vm", "--vm":
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		case "-daemon", "--daemon", "-user", "--user":
			flags = append(flags, arg)
		default:
			if strings.HasPrefix(arg, "-vm=") || strings.HasPrefix(arg, "--vm=") {
				flags = append(flags, arg)
			} else {
				rest = append(rest, arg)
			}
		}
	}
	return append(flags, rest...)
}

func parseCpSpec(src, dst string) (cpSpec, error) {
	return parseCpSpecForVM(src, dst, "")
}

func parseCpSpecForVM(src, dst, vmFlag string) (cpSpec, error) {
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
		if err := validateCpVMFlag(vmFlag, srcVM); err != nil {
			return cpSpec{}, err
		}
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
	if err := validateCpVMFlag(vmFlag, dstVM); err != nil {
		return cpSpec{}, err
	}
	return cpSpec{
		Direction: cpHostToGuest,
		VM:        dstVM,
		HostPath:  hostPath,
		GuestPath: dstPath,
	}, nil
}

func validateCpVMFlag(flagVM, endpointVM string) error {
	flagVM = strings.TrimSpace(flagVM)
	if flagVM == "" {
		return nil
	}
	if strings.Contains(flagVM, "/") {
		return fmt.Errorf("cp: invalid VM name %q", flagVM)
	}
	if flagVM != endpointVM {
		return fmt.Errorf("cp: -vm %q does not match remote endpoint VM %q", flagVM, endpointVM)
	}
	return nil
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

// routableCpAgent is implemented by cp agents that can target a specific guest
// agent (daemon vs user). The control-socket agent supports it; test doubles
// need not.
type routableCpAgent interface {
	setRoute(controlpb.AgentRoute)
}

func newControlCpAgent(vm string) cpAgent {
	socketPath := filepath.Join(vmconfig.BaseDir(), vm, "control.sock")
	client := NewControlClient(socketPath)
	client.SetTimeout(10 * time.Minute)
	return &controlCpAgent{client: client, vm: vm}
}

func (a *controlCpAgent) setRoute(r controlpb.AgentRoute) { a.route = r }

func (a *controlCpAgent) CopyToGuest(_ context.Context, hostPath, guestPath string) error {
	if _, err := requireExistingVMForControl(a.vm); err != nil {
		return err
	}
	req := &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{AgentCp: &controlpb.AgentCopyCommand{
			HostPath:  hostPath,
			GuestPath: guestPath,
			ToGuest:   true,
			Route:     a.route,
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

func (a *controlCpAgent) CopyFromGuest(_ context.Context, guestPath, hostPath string) error {
	if _, err := requireExistingVMForControl(a.vm); err != nil {
		return err
	}
	req := &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{AgentCp: &controlpb.AgentCopyCommand{
			HostPath:  hostPath,
			GuestPath: guestPath,
			ToGuest:   false,
			Route:     a.route,
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
