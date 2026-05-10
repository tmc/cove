// port_forward.go - Package-main glue for port forwarding.
//
// The PortForwardManager type and its forward goroutines live in
// internal/controlserver. This file keeps the CLI flag types, the
// startup wire-up, the *ControlServer-bound dispatcher, and the
// host-side vsock listener primitives used by reverse forwards.
package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/vz-macos/internal/controlserver"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// PortForwardManager is an alias of controlserver.PortForwardManager.
// The type lives in internal/controlserver so the network bridge can
// hold it without crossing the package-main boundary.
type PortForwardManager = controlserver.PortForwardManager

// NewPortForwardManager mirrors controlserver.NewPortForwardManager so
// the package-main name continues to resolve.
var NewPortForwardManager = controlserver.NewPortForwardManager

type portForwardSpecs []portForwardSpec

type portForwardSpec struct {
	HostPort  int
	GuestPort uint32
}

func (s *portForwardSpecs) Set(value string) error {
	spec, err := parsePortForwardSpec(value)
	if err != nil {
		return err
	}
	*s = append(*s, spec)
	return nil
}

func (s *portForwardSpecs) String() string {
	if s == nil || len(*s) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*s))
	for _, spec := range *s {
		parts = append(parts, fmt.Sprintf("%d:%d", spec.HostPort, spec.GuestPort))
	}
	return strings.Join(parts, ",")
}

func parsePortForwardSpec(value string) (portForwardSpec, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return portForwardSpec{}, fmt.Errorf("expected hostPort:guestVsockPort, got %q", value)
	}
	hostPort, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return portForwardSpec{}, fmt.Errorf("invalid host port %q: %w", parts[0], err)
	}
	if hostPort == 0 {
		return portForwardSpec{}, fmt.Errorf("invalid host port %q: must be > 0", parts[0])
	}
	guestPort, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return portForwardSpec{}, fmt.Errorf("invalid guest vsock port %q: %w", parts[1], err)
	}
	if guestPort == 0 {
		return portForwardSpec{}, fmt.Errorf("invalid guest vsock port %q: must be > 0", parts[1])
	}
	return portForwardSpec{HostPort: int(hostPort), GuestPort: uint32(guestPort)}, nil
}

func startConfiguredPortForwards(s *ControlServer) error {
	if s == nil || len(startupPortForwards) == 0 {
		return nil
	}
	manager := s.portForwardManager()
	connector := newControlServerGuestConnector(s)
	for _, spec := range startupPortForwards {
		if err := manager.Start(connector, spec.HostPort, spec.GuestPort); err != nil {
			return fmt.Errorf("port-forward %d:%d: %w", spec.HostPort, spec.GuestPort, err)
		}
	}
	return nil
}

// handlePortForward dispatches port-forward commands.
func (s *ControlServer) handlePortForward(cmd *controlpb.PortForwardCommand) *controlpb.ControlResponse {
	portForwards := s.portForwardManager()

	switch cmd.Action {
	case "start":
		if cmd.HostPort == 0 || cmd.GuestPort == 0 {
			return &controlpb.ControlResponse{Error: "host_port and guest_port required"}
		}
		if err := portForwards.Start(newControlServerGuestConnector(s), int(cmd.HostPort), cmd.GuestPort); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("forwarding localhost:%d -> vsock:%d", cmd.HostPort, cmd.GuestPort)
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	case "start-udp":
		if cmd.HostPort == 0 || cmd.GuestPort == 0 {
			return &controlpb.ControlResponse{Error: "host_port and guest_port required"}
		}
		if err := portForwards.StartUDP(newControlServerGuestConnector(s), int(cmd.HostPort), cmd.GuestPort); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("forwarding udp localhost:%d -> vsock:%d", cmd.HostPort, cmd.GuestPort)
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	case "start-reverse":
		if cmd.HostPort == 0 || cmd.GuestPort == 0 {
			return &controlpb.ControlResponse{Error: "host_port and guest_port required"}
		}
		if err := portForwards.StartReverse(int(cmd.HostPort), int(cmd.GuestPort)); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("reverse forwarding vm:%d -> localhost:%d", cmd.GuestPort, cmd.HostPort)
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	case "start-reverse-udp":
		if cmd.HostPort == 0 || cmd.GuestPort == 0 {
			return &controlpb.ControlResponse{Error: "host_port and guest_port required"}
		}
		if err := portForwards.StartReverseUDP(int(cmd.HostPort), int(cmd.GuestPort)); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("reverse forwarding udp vm:%d -> localhost:%d", cmd.GuestPort, cmd.HostPort)
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	case "stop":
		if cmd.HostPort == 0 {
			return &controlpb.ControlResponse{Error: "host_port required"}
		}
		if err := portForwards.Stop(int(cmd.HostPort)); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("stopped forwarding localhost:%d", cmd.HostPort)
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	case "list":
		forwards := portForwards.List()
		msg := "no active port forwards"
		if len(forwards) > 0 {
			msg = fmt.Sprintf("%d active:\n%s", len(forwards), joinLines(forwards))
		}
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown port-forward action: %s", cmd.Action)}
	}
}

func (s *ControlServer) portForwardManager() *PortForwardManager {
	return s.network.PortForwards()
}

func (s *ControlServer) clearPortForwardManager() *PortForwardManager {
	return s.network.ClearPortForwards()
}

type hostVsockListener struct {
	file *os.File
}

func (l *hostVsockListener) Accept() (net.Conn, error) {
	fd, _, errno := syscall.Syscall(syscall.SYS_ACCEPT, l.file.Fd(), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	f := os.NewFile(uintptr(fd), "host-vsock-conn")
	return &hostVsockConn{file: f}, nil
}

func (l *hostVsockListener) Close() error {
	return l.file.Close()
}

func (l *hostVsockListener) Addr() net.Addr {
	return hostVsockAddr{}
}

type hostVsockConn struct {
	file *os.File
}

func (c *hostVsockConn) Read(b []byte) (int, error)  { return c.file.Read(b) }
func (c *hostVsockConn) Write(b []byte) (int, error) { return c.file.Write(b) }
func (c *hostVsockConn) Close() error                { return c.file.Close() }
func (c *hostVsockConn) LocalAddr() net.Addr         { return hostVsockAddr{} }
func (c *hostVsockConn) RemoteAddr() net.Addr        { return hostVsockAddr{} }
func (c *hostVsockConn) SetDeadline(t time.Time) error {
	return c.file.SetDeadline(t)
}
func (c *hostVsockConn) SetReadDeadline(t time.Time) error {
	return c.file.SetReadDeadline(t)
}
func (c *hostVsockConn) SetWriteDeadline(t time.Time) error {
	return c.file.SetWriteDeadline(t)
}

type hostVsockAddr struct{}

func (hostVsockAddr) Network() string { return "vsock" }
func (hostVsockAddr) String() string  { return "vsock" }

func joinLines(ss []string) string {
	result := ""
	for _, s := range ss {
		result += "  " + s + "\n"
	}
	return result
}
