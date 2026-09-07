package restore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/apple/x/iosrestore"
	"github.com/tmc/apple/x/usbmux"
)

type probeMux struct {
	devices    []usbmux.Device
	identities map[uint32]uint64
	fail       bool
	mu         sync.Mutex
	seen       []uint32
}

func (m *probeMux) List(context.Context) ([]usbmux.Device, error) { return m.devices, nil }
func (m *probeMux) Dial(ctx context.Context, id uint32, port uint16) (net.Conn, error) {
	if port != 62078 {
		return nil, fmt.Errorf("unexpected port %d", port)
	}
	m.mu.Lock()
	m.seen = append(m.seen, id)
	m.mu.Unlock()
	if m.fail {
		return nil, errors.New("probe unavailable")
	}
	host, peer := net.Pipe()
	go func() {
		defer peer.Close()
		peer.SetDeadline(time.Now().Add(time.Second))
		query, err := iosrestore.Receive(peer)
		if err != nil {
			return
		}
		if query["Request"] != "QueryType" {
			return
		}
		service := "com.apple.mobile.restored"
		if m.identities[id] == 0 {
			service = "com.apple.mobile.lockdown"
		}
		if err := iosrestore.Send(peer, map[string]any{"Type": service, "RestoreProtocolVersion": 15}); err != nil {
			return
		}
		if service != "com.apple.mobile.restored" {
			return
		}
		query, err = iosrestore.Receive(peer)
		if err != nil {
			return
		}
		if query["Request"] != "QueryValue" || query["QueryKey"] != "HardwareInfo" {
			return
		}
		iosrestore.Send(peer, map[string]any{"HardwareInfo": map[string]any{"UniqueChipID": m.identities[id]}})
		var b [1]byte
		peer.Read(b[:]) // Wait for the probing client to close the service.
	}()
	return host, nil
}

func TestProbeIdentity(t *testing.T) {
	for _, tt := range []struct {
		name    string
		ids     map[uint32]uint64
		serial  string
		wantID  uint32
		wantErr string
	}{
		{"exact", map[uint32]uint64{1: 42, 2: 17}, "", 1, ""},
		{"wrong", map[uint32]uint64{1: 17, 2: 18}, "", 0, "no restored"},
		{"duplicate", map[uint32]uint64{1: 42, 2: 42}, "", 0, "multiple restored"},
		{"serial narrows", map[uint32]uint64{1: 42, 2: 42}, "second", 2, ""},
		{"serial not identity", map[uint32]uint64{1: 42, 2: 17}, "second", 0, "no restored"},
		{"skip lockdown", map[uint32]uint64{1: 0, 2: 42}, "", 2, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &probeMux{devices: []usbmux.Device{{ID: 1, Serial: "first", ConnectionType: "USB"}, {ID: 2, Serial: "second", ConnectionType: "USB"}, {ID: 3, Serial: "wireless", ConnectionType: "Network"}}, identities: tt.ids}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := probe(ctx, m, Target{ECID: 42, Serial: tt.serial})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatal(got, err)
				}
			} else if err != nil || got.Attachment.ID != tt.wantID || got.Restore.ECID != 42 {
				t.Fatal(got, err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			for _, id := range m.seen {
				if id == 3 {
					t.Fatal("probed network device")
				}
			}
		})
	}
}

func TestProbeFailure(t *testing.T) {
	m := &probeMux{devices: []usbmux.Device{{ID: 1, Serial: "first", ConnectionType: "USB"}}, fail: true}
	if _, err := probe(context.Background(), m, Target{ECID: 42}); err == nil || !strings.Contains(err.Error(), "probe unavailable") {
		t.Fatal(err)
	}
	if _, err := probe(context.Background(), m, Target{}); err == nil {
		t.Fatal("accepted missing ECID")
	}
}

func ExampleProbe() {
	_, err := Probe(context.Background(), usbmux.Client{}, Target{})
	fmt.Println(err)
	// Output: restore ECID is required
}
