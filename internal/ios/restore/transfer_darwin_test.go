//go:build darwin

package restore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/apple/x/irecovery"
)

type fakeComponentConn struct {
	t         *testing.T
	directory string
	info      irecovery.Info
	events    *[]string
	fail      string
	reads     int
}

func (c *fakeComponentConn) ReadInfo(context.Context) (irecovery.Info, error) {
	*c.events = append(*c.events, "observe:"+c.info.Device.Mode)
	c.reads++
	if c.fail == "nonce" && c.reads == 2 {
		c.info.APNonce = []byte{99}
	}
	return c.info, nil
}
func (c *fakeComponentConn) mutation(action string) error {
	c.t.Helper()
	state := readTransfer(c.t, c.directory)
	if state.Action != action || state.Status != "active" {
		c.t.Fatalf("device operation before durable intent: %#v", state)
	}
	*c.events = append(*c.events, action)
	if c.fail == action {
		return fmt.Errorf("injected %s failure", action)
	}
	return nil
}
func (c *fakeComponentConn) Upload(_ context.Context, b []byte) error {
	if state := readTransfer(c.t, c.directory); state.PersonalizedSHA256 != transferHash(b) {
		c.t.Fatal("wrong image hash recorded")
	}
	return c.mutation("upload")
}
func (c *fakeComponentConn) FinalizeDFU(context.Context) error { return c.mutation("finalize") }
func (c *fakeComponentConn) SendCommand(_ context.Context, command string, request uint8) error {
	if command == "go" && request != 1 || command != "go" && request != 0 {
		c.t.Fatal("incorrect command request")
	}
	return c.mutation("command")
}
func (c *fakeComponentConn) WaitDisconnected(context.Context) error { return c.mutation("disconnect") }
func (c *fakeComponentConn) Close() error {
	*c.events = append(*c.events, "close:"+c.info.Device.Mode)
	return nil
}
func readTransfer(t *testing.T, directory string) transferRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(directory, "transfer.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state transferRecord
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
func transferInfo(mode string) irecovery.Info {
	board, cpfm, ibfl := uint32(0x90), uint32(3), uint32(4)
	return irecovery.Info{Device: irecovery.Device{ECID: 1<<63 | 17, CPID: 0xfe01, Mode: mode}, BoardID: &board, CPFM: &cpfm, IBFL: &ibfl, APNonce: []byte{1, 2}, SEPNonce: []byte{3, 4}}
}
func TestTransferComponent(t *testing.T) {
	for _, tt := range []struct {
		name, mode, command, next, fail string
		rotate                          bool
		want                            []string
	}{
		{name: "DFU", mode: "dfu", next: "recovery", want: []string{"open:dfu", "observe:dfu", "sign", "observe:dfu", "upload", "finalize", "close:dfu", "open:recovery", "observe:recovery", "close:recovery"}},
		{name: "go", mode: "recovery", command: "go", next: "recovery", want: []string{"open:recovery", "observe:recovery", "sign", "observe:recovery", "upload", "command", "disconnect", "close:recovery", "open:recovery", "observe:recovery", "close:recovery"}},
		{name: "load", mode: "recovery", command: "devicetree", want: []string{"open:recovery", "observe:recovery", "sign", "observe:recovery", "upload", "command", "close:recovery"}},
		{name: "nonce rotates after reset", mode: "dfu", next: "recovery", rotate: true},
		{name: "nonce changed", mode: "dfu", next: "recovery", fail: "nonce"},
		{name: "upload failed", mode: "dfu", next: "recovery", fail: "upload"},
		{name: "finalize failed", mode: "dfu", next: "recovery", fail: "finalize"},
		{name: "command failed", mode: "recovery", command: "go", next: "recovery", fail: "command"},
		{name: "disconnect failed", mode: "recovery", command: "go", next: "recovery", fail: "disconnect"},
		{name: "wrong reconnect", mode: "dfu", next: "recovery", fail: "identity"},
		{name: "still ROM", mode: "dfu", next: "recovery", fail: "ROM"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			build, _ := signingFixture()
			payload, _ := hex.DecodeString(syntheticPayload)
			plan := ComponentTransfer{Library: "test", ECID: 1<<63 | 17, Identity: build, Component: "iBSS", Payload: payload, Mode: tt.mode, NextMode: tt.next, Command: tt.command}
			events := []string{}
			opens := 0
			connect := func(_ context.Context, mode string) (componentConnection, error) {
				events = append(events, "open:"+mode)
				opens++
				info := transferInfo(mode)
				if opens > 1 && tt.rotate {
					info.APNonce = []byte{99}
					info.SEPNonce = []byte{98}
				}
				if opens > 1 && tt.fail == "identity" {
					info.Device.ECID++
				}
				if opens > 1 && tt.fail == "ROM" {
					info.Device.Serial = "SRTG:[iBoot]"
				}
				return &fakeComponentConn{t: t, directory: directory, info: info, events: &events, fail: tt.fail}, nil
			}
			sign := func(_ context.Context, _ map[string]any, device SigningDevice) (map[string]any, error) {
				events = append(events, "sign")
				if device.ECID != plan.ECID {
					t.Fatal("wrong signing target")
				}
				ticket, _ := hex.DecodeString(syntheticTicket)
				return map[string]any{"ApImg4Ticket": ticket}, nil
			}
			err := transferComponent(context.Background(), directory, plan, connect, sign, filepath.Join(directory, "device-locks"))
			state := readTransfer(t, directory)
			if tt.fail == "" {
				if err != nil || state.Status != "complete" || (tt.want != nil && !reflect.DeepEqual(events, tt.want)) {
					t.Fatalf("err=%v state=%#v events=%v", err, state, events)
				}
			} else {
				if err == nil || state.Status != "failed" || state.Error == "" {
					t.Fatalf("err=%v state=%#v", err, state)
				}
				if tt.fail == "nonce" {
					for _, event := range events {
						if event == "upload" {
							t.Fatal("uploaded with stale nonce")
						}
					}
				}
			}
			oldOpens := opens
			if err := transferComponent(context.Background(), directory, plan, connect, sign, filepath.Join(directory, "device-locks")); err == nil || !strings.Contains(err.Error(), "already recorded") || opens != oldOpens {
				t.Fatalf("replayed recorded attempt: %v", err)
			}
		})
	}
}
func ExampleTransferComponent() {
	err := TransferComponent(context.Background(), "", ComponentTransfer{}, nil)
	fmt.Println(err)
	// Output: component transfer requires directory, library, ECID, component and payload
}

func TestTransferDeviceLock(t *testing.T) {
	root := t.TempDir()
	first, err := lockTransferDevice(root, 17)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockTransferDevice(root, 17); err == nil {
		second.Close()
		t.Fatal("concurrent target lock succeeded")
	}
	other, err := lockTransferDevice(root, 18)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	first.Close()
	again, err := lockTransferDevice(root, 17)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestTransferLocalPolicy(t *testing.T) {
	for _, name := range []string{"match", "rejected binding", "stale AP ticket", "changed nonce"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			build, _ := signingFixture()
			info := transferInfo("recovery")
			device, err := signingObservation(info, info.Device.ECID)
			if err != nil {
				t.Fatal(err)
			}
			plan := ComponentTransfer{Library: "test", ECID: device.ECID, Identity: build,
				Component: "Ap,LocalPolicy", Mode: "recovery", Command: "lpolrestore",
				NextStageTicket: nextPolicyTicket(device), NextStageComponents: []string{"iBSS"}}
			if name == "stale AP ticket" {
				info.APNonce = []byte{99}
			}
			events := []string{}
			conn := &fakeComponentConn{t: t, directory: directory, info: info, events: &events}
			if name == "changed nonce" {
				conn.fail = "nonce"
			}
			client := localPolicyServer(t, name == "rejected binding")
			err = transferComponent(context.Background(), directory, plan,
				func(context.Context, string) (componentConnection, error) { return conn, nil },
				func(ctx context.Context, identity map[string]any, device SigningDevice) (map[string]any, error) {
					return signComponent(ctx, client, plan, identity, device)
				}, filepath.Join(directory, "device-locks"))
			state := readTransfer(t, directory)
			if state.NextStageSHA256 != transferHash(plan.NextStageTicket) || state.PayloadSHA256 != transferHash(EmptyLocalPolicy()) {
				t.Fatal("missing durable local-policy provenance")
			}
			if name == "match" {
				if err != nil || state.Status != "complete" || !reflect.DeepEqual(events, []string{"observe:recovery", "observe:recovery", "upload", "command", "close:recovery"}) {
					t.Fatalf("got %v, %#v, %v", err, state, events)
				}
			} else {
				if err == nil || state.Status != "failed" {
					t.Fatalf("got %v, %#v", err, state)
				}
				for _, event := range events {
					if event == "upload" || event == "command" {
						t.Fatal("mutated device after signing mismatch")
					}
				}
			}
		})
	}
}

func TestRetainedAPResponse(t *testing.T) {
	build, device := signingFixture()
	ticket := nextPolicyTicket(device)
	plan := ComponentTransfer{Component: "iBSS", SigningResponse: map[string]any{
		"ApImg4Ticket": ticket, "iBSS-TBM": map[string]any{"BNCN": make([]byte, 8)},
	}}
	response, err := signComponent(context.Background(), nil, plan, build, device)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response, plan.SigningResponse) {
		t.Fatal("retained response changed")
	}
	response["ApImg4Ticket"].([]byte)[0] = 0
	if ticket[0] == 0 {
		t.Fatal("retained response aliased input")
	}
	device.APNonce = []byte{99}
	if _, err := signComponent(context.Background(), nil, plan, build, device); err == nil {
		t.Fatal("reused AP response with stale nonce")
	}
}
