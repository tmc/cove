// port_forward.go - Host-to-guest and guest-to-host TCP/UDP port
// forwarding manager.
//
// PortForwardManager owns the lifecycle of a set of forwards. The
// platform-specific host vsock listener is injected via
// ListenHostVsock so this package stays portable.
package controlserver

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// ListenHostVsock binds a host-side vsock listener on port. Set by
// package main during startup. PortForwardManager.StartReverse and
// StartReverseUDP fail with a clear error if it is nil.
var ListenHostVsock func(port uint32) (net.Listener, error)

// ForwardRelayBasePort and ForwardRelayPortWindow scope the host
// vsock relay range used by reverse port forwards. Match the values
// in package main's forward.go.
var (
	ForwardRelayBasePort   = 20000
	ForwardRelayPortWindow = 20000
)

func relayPortFor(port int) uint32 {
	return uint32(ForwardRelayBasePort + port%ForwardRelayPortWindow)
}

// PortForward represents an active host TCP -> guest vsock forwarding rule.
type PortForward struct {
	HostPort  int
	GuestPort uint32
	listener  net.Listener
	connector GuestConnector
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
	udp      map[int]*UDPForward
	udpRev   map[int]*ReverseUDPForward
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
		udp:      make(map[int]*UDPForward),
		udpRev:   make(map[int]*ReverseUDPForward),
	}
}

// Start begins forwarding from hostPort to guest vsock guestPort.
func (m *PortForwardManager) Start(connector GuestConnector, hostPort int, guestPort uint32) error {
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

func (m *PortForwardManager) StartUDP(connector GuestConnector, hostPort int, guestPort uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.udp[hostPort]; exists {
		return fmt.Errorf("host udp port %d already forwarded", hostPort)
	}
	if connector == nil {
		return fmt.Errorf("guest connector unavailable")
	}
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort)))
	if err != nil {
		return fmt.Errorf("resolve udp %d: %w", hostPort, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen udp %d: %w", hostPort, err)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	uf := &UDPForward{HostPort: hostPort, GuestPort: guestPort, conn: conn, connector: connector, cancel: cancel}
	m.udp[hostPort] = uf
	go uf.serve(ctx)
	fmt.Printf("[port-forward] udp localhost:%d -> vsock:%d\n", hostPort, guestPort)
	return nil
}

// StartReverse begins forwarding from a guest TCP port to a host TCP port.
func (m *PortForwardManager) StartReverse(hostPort int, guestPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.reverse[guestPort]; exists {
		return fmt.Errorf("guest port %d already reverse-forwarded", guestPort)
	}
	if ListenHostVsock == nil {
		return fmt.Errorf("host vsock listener not configured")
	}
	relayPort := uint32(ForwardRelayBasePort + guestPort%ForwardRelayPortWindow)
	ln, err := ListenHostVsock(relayPort)
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

func (m *PortForwardManager) StartReverseUDP(hostPort int, guestPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.udpRev[guestPort]; exists {
		return fmt.Errorf("guest udp port %d already reverse-forwarded", guestPort)
	}
	if ListenHostVsock == nil {
		return fmt.Errorf("host vsock listener not configured")
	}
	relayPort := relayPortFor(guestPort)
	ln, err := ListenHostVsock(relayPort)
	if err != nil {
		return fmt.Errorf("listen vsock:%d: %w", relayPort, err)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	rf := &ReverseUDPForward{HostPort: hostPort, GuestPort: guestPort, RelayPort: relayPort, listener: ln, cancel: cancel}
	m.udpRev[guestPort] = rf
	go rf.serve(ctx)
	fmt.Printf("[port-forward] reverse udp vsock:%d -> localhost:%d\n", relayPort, hostPort)
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
	for _, uf := range m.udp {
		uf.mu.Lock()
		conns := uf.conns
		uf.mu.Unlock()
		result = append(result, fmt.Sprintf("udp localhost:%d -> vsock:%d (%d packets)", uf.HostPort, uf.GuestPort, conns))
	}
	for _, rf := range m.udpRev {
		rf.mu.Lock()
		conns := rf.conns
		rf.mu.Unlock()
		result = append(result, fmt.Sprintf("udp vm:%d -> localhost:%d via vsock:%d (%d packets)", rf.GuestPort, rf.HostPort, rf.RelayPort, conns))
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
	for hp, uf := range m.udp {
		uf.cancel()
		uf.conn.Close()
		delete(m.udp, hp)
	}
	for gp, rf := range m.udpRev {
		rf.cancel()
		rf.listener.Close()
		delete(m.udpRev, gp)
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

type UDPForward struct {
	HostPort  int
	GuestPort uint32
	conn      *net.UDPConn
	connector GuestConnector
	cancel    context.CancelFunc
	mu        sync.Mutex
	conns     int
}

func (uf *UDPForward) serve(ctx context.Context) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := uf.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Printf("[port-forward] udp read localhost:%d: %v\n", uf.HostPort, err)
			return
		}
		pkt := append([]byte(nil), buf[:n]...)
		go uf.handlePacket(addr, pkt)
	}
}

func (uf *UDPForward) handlePacket(addr *net.UDPAddr, pkt []byte) {
	uf.mu.Lock()
	uf.conns++
	uf.mu.Unlock()
	defer func() {
		uf.mu.Lock()
		uf.conns--
		uf.mu.Unlock()
	}()
	conn, err := uf.connector.ConnectToGuestPort(uf.GuestPort)
	if err != nil {
		fmt.Printf("[port-forward] udp vsock connect %d: %v\n", uf.GuestPort, err)
		return
	}
	defer conn.Close()
	if err := writeUDPFrame(conn, pkt); err != nil {
		fmt.Printf("[port-forward] udp write frame: %v\n", err)
		return
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		reply, err := readUDPFrame(conn)
		if err != nil {
			return
		}
		uf.conn.WriteToUDP(reply, addr)
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

type ReverseUDPForward struct {
	HostPort  int
	GuestPort int
	RelayPort uint32
	listener  net.Listener
	cancel    context.CancelFunc
	mu        sync.Mutex
	conns     int
}

func (rf *ReverseUDPForward) serve(ctx context.Context) {
	for {
		conn, err := rf.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				fmt.Printf("[port-forward] accept reverse udp vsock:%d: %v\n", rf.RelayPort, err)
				return
			}
		}
		go rf.handleConn(conn)
	}
}

func (rf *ReverseUDPForward) handleConn(vsockConn net.Conn) {
	defer vsockConn.Close()
	rf.mu.Lock()
	rf.conns++
	rf.mu.Unlock()
	defer func() {
		rf.mu.Lock()
		rf.conns--
		rf.mu.Unlock()
	}()
	pkt, err := readUDPFrame(vsockConn)
	if err != nil {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(rf.HostPort)))
	if err != nil {
		fmt.Printf("[port-forward] reverse udp resolve localhost:%d: %v\n", rf.HostPort, err)
		return
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		fmt.Printf("[port-forward] reverse udp dial localhost:%d: %v\n", rf.HostPort, err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write(pkt); err != nil {
		return
	}
	buf := make([]byte, 65535)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		if err := writeUDPFrame(vsockConn, buf[:n]); err != nil {
			return
		}
	}
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

	if Verbose {
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

func writeUDPFrame(w io.Writer, pkt []byte) error {
	if len(pkt) > 65535 {
		return fmt.Errorf("udp packet too large: %d", len(pkt))
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(pkt)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(pkt)
	return err
}

func readUDPFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	pkt := make([]byte, n)
	_, err := io.ReadFull(r, pkt)
	return pkt, err
}
