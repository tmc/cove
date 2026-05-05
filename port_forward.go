package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

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
	if err != nil || hostPort == 0 {
		return portForwardSpec{}, fmt.Errorf("invalid host port %q", parts[0])
	}
	guestPort, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || guestPort == 0 {
		return portForwardSpec{}, fmt.Errorf("invalid guest vsock port %q", parts[1])
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

// PortForward represents an active host TCP -> guest vsock forwarding rule.
type PortForward struct {
	HostPort  int
	GuestPort uint32
	listener  net.Listener
	connector guestPortConnector
	cancel    context.CancelFunc
	mu        sync.Mutex
	conns     int // active connection count
}

// PortForwardManager tracks active port forwards.
type PortForwardManager struct {
	mu       sync.Mutex
	ctx      context.Context
	forwards map[int]*PortForward // keyed by host port
	reverse  map[int]*ReversePortForward
}

// NewPortForwardManager creates a new manager.
func NewPortForwardManager(ctx context.Context) *PortForwardManager {
	if ctx == nil {
		ctx = context.Background()
	}
	return &PortForwardManager{
		ctx:      ctx,
		forwards: make(map[int]*PortForward),
		reverse:  make(map[int]*ReversePortForward),
	}
}

// Start begins forwarding from hostPort to guest vsock guestPort.
func (m *PortForwardManager) Start(connector guestPortConnector, hostPort int, guestPort uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.forwards[hostPort]; exists {
		return fmt.Errorf("host port %d already forwarded", hostPort)
	}
	if connector == nil {
		return fmt.Errorf("guest connector unavailable")
	}

	addr := fmt.Sprintf("localhost:%d", hostPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(m.ctx)
	pf := &PortForward{
		HostPort:  hostPort,
		GuestPort: guestPort,
		listener:  ln,
		connector: connector,
		cancel:    cancel,
	}
	m.forwards[hostPort] = pf

	go pf.serve(ctx)

	fmt.Printf("[port-forward] localhost:%d -> vsock:%d\n", hostPort, guestPort)
	return nil
}

// StartReverse begins forwarding from a guest TCP port to a host TCP port.
func (m *PortForwardManager) StartReverse(hostPort int, guestPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.reverse[guestPort]; exists {
		return fmt.Errorf("guest port %d already reverse-forwarded", guestPort)
	}
	relayPort := uint32(forwardRelayBasePort + guestPort%forwardRelayPortWindow)
	ln, err := listenHostVsock(relayPort)
	if err != nil {
		return fmt.Errorf("listen vsock:%d: %w", relayPort, err)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	rf := &ReversePortForward{
		HostPort:  hostPort,
		GuestPort: guestPort,
		RelayPort: relayPort,
		listener:  ln,
		cancel:    cancel,
	}
	m.reverse[guestPort] = rf
	go rf.serve(ctx)
	fmt.Printf("[port-forward] reverse vsock:%d -> localhost:%d\n", relayPort, hostPort)
	return nil
}

// Stop removes a port forward by host port.
func (m *PortForwardManager) Stop(hostPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pf, ok := m.forwards[hostPort]
	if !ok {
		return fmt.Errorf("no forward on host port %d", hostPort)
	}

	pf.cancel()
	pf.listener.Close()
	delete(m.forwards, hostPort)
	return nil
}

// List returns descriptions of all active forwards.
func (m *PortForwardManager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []string
	for _, pf := range m.forwards {
		pf.mu.Lock()
		conns := pf.conns
		pf.mu.Unlock()
		result = append(result, fmt.Sprintf("localhost:%d -> vsock:%d (%d active)", pf.HostPort, pf.GuestPort, conns))
	}
	for _, rf := range m.reverse {
		rf.mu.Lock()
		conns := rf.conns
		rf.mu.Unlock()
		result = append(result, fmt.Sprintf("vm:%d -> localhost:%d via vsock:%d (%d active)", rf.GuestPort, rf.HostPort, rf.RelayPort, conns))
	}
	return result
}

// StopAll shuts down all active forwards.
func (m *PortForwardManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for hp, pf := range m.forwards {
		pf.cancel()
		pf.listener.Close()
		delete(m.forwards, hp)
	}
	for gp, rf := range m.reverse {
		rf.cancel()
		rf.listener.Close()
		delete(m.reverse, gp)
	}
}

func (pf *PortForward) serve(ctx context.Context) {
	for {
		conn, err := pf.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				fmt.Printf("[port-forward] accept localhost:%d: %v\n", pf.HostPort, err)
				return
			}
		}
		go pf.handleConn(ctx, conn)
	}
}

// ReversePortForward represents an active guest TCP -> host TCP forwarding rule.
type ReversePortForward struct {
	HostPort  int
	GuestPort int
	RelayPort uint32
	listener  net.Listener
	cancel    context.CancelFunc
	mu        sync.Mutex
	conns     int
}

func (rf *ReversePortForward) serve(ctx context.Context) {
	for {
		conn, err := rf.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				fmt.Printf("[port-forward] accept reverse vsock:%d: %v\n", rf.RelayPort, err)
				return
			}
		}
		go rf.handleConn(ctx, conn)
	}
}

func (rf *ReversePortForward) handleConn(ctx context.Context, guestConn net.Conn) {
	defer guestConn.Close()
	rf.mu.Lock()
	rf.conns++
	rf.mu.Unlock()
	defer func() {
		rf.mu.Lock()
		rf.conns--
		rf.mu.Unlock()
	}()

	hostConn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(rf.HostPort)))
	if err != nil {
		fmt.Printf("[port-forward] reverse dial localhost:%d: %v\n", rf.HostPort, err)
		return
	}
	defer hostConn.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(hostConn, guestConn)
		if tc, ok := hostConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		close(done)
	}()
	io.Copy(guestConn, hostConn)
	if d, ok := guestConn.(interface{ SetWriteDeadline(time.Time) error }); ok {
		d.SetWriteDeadline(time.Now())
	}
	<-done
}

func (pf *PortForward) handleConn(ctx context.Context, hostConn net.Conn) {
	defer hostConn.Close()

	pf.mu.Lock()
	pf.conns++
	pf.mu.Unlock()
	defer func() {
		pf.mu.Lock()
		pf.conns--
		pf.mu.Unlock()
	}()

	vsockConn, err := pf.connector.ConnectToGuestPort(pf.GuestPort)
	if err != nil {
		fmt.Printf("[port-forward] vsock connect %d: %v\n", pf.GuestPort, err)
		return
	}
	defer vsockConn.Close()

	if verbose {
		fmt.Printf("[port-forward] new conn: %s -> vsock:%d\n", hostConn.RemoteAddr(), pf.GuestPort)
	}

	// Bidirectional relay with idle timeout.
	done := make(chan struct{})
	go func() {
		io.Copy(vsockConn, hostConn)
		if d, ok := vsockConn.(interface{ SetWriteDeadline(time.Time) error }); ok {
			d.SetWriteDeadline(time.Now())
		}
		close(done)
	}()
	io.Copy(hostConn, vsockConn)
	if tc, ok := hostConn.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
	<-done
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.portForwards == nil {
		s.portForwards = NewPortForwardManager(s.lifecycleContext())
	}
	return s.portForwards
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
