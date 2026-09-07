package restore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tmc/apple/x/iosrestore"
)

func sessionPeer(conn net.Conn, ecid uint64) error {
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	request, err := iosrestore.Receive(conn)
	if err != nil {
		return err
	}
	if request["Request"] != "QueryType" {
		return fmt.Errorf("missing query type")
	}
	if err := iosrestore.Send(conn, map[string]any{"Type": "com.apple.mobile.restored", "RestoreProtocolVersion": uint64(19)}); err != nil {
		return err
	}
	request, err = iosrestore.Receive(conn)
	if err != nil {
		return err
	}
	if request["QueryKey"] != "HardwareInfo" {
		return fmt.Errorf("missing hardware query")
	}
	return iosrestore.Send(conn, map[string]any{"HardwareInfo": map[string]any{"UniqueChipID": ecid}})
}

func expectStart(conn net.Conn) error {
	request, err := iosrestore.Receive(conn)
	if err != nil {
		return err
	}
	options, _ := request["RestoreOptions"].(map[string]any)
	if request["Request"] != "StartRestore" || request["RestoreProtocolVersion"] != int64(19) || options["TestRestore"] != true {
		return fmt.Errorf("incorrect StartRestore: %#v", request)
	}
	return nil
}

func TestRestoredSession(t *testing.T) {
	build, device := signingFixture()
	_, want, err := APSigningRequest(build, device, []string{"iBSS"})
	if err != nil {
		t.Fatal(err)
	}
	data := &SessionData{APTicket: nextPolicyTicket(device), APRequirements: want}
	host, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		if err := sessionPeer(peer, device.ECID); err != nil {
			done <- err
			return
		}
		if err := expectStart(peer); err != nil {
			done <- err
			return
		}
		if err := iosrestore.Send(peer, map[string]any{"MsgType": "DataRequestMsg", "DataType": "RootTicket"}); err != nil {
			done <- err
			return
		}
		response, err := iosrestore.Receive(peer)
		if err != nil {
			done <- err
			return
		}
		if _, ok := response["RootTicketData"].([]byte); !ok {
			done <- fmt.Errorf("missing root ticket")
			return
		}
		if err := iosrestore.Send(peer, map[string]any{"MsgType": "ProgressMsg", "Progress": int64(50)}); err != nil {
			done <- err
			return
		}
		if err := iosrestore.Send(peer, map[string]any{"MsgType": "StatusMsg", "Status": int64(0)}); err != nil {
			done <- err
			return
		}
		response, err = iosrestore.Receive(peer)
		if err == nil && response["MsgType"] != "ReceivedFinalStatusMsg" {
			err = fmt.Errorf("missing final acknowledgement")
		}
		done <- err
	}()
	events := 0
	err = RunRestored(ctx, host, device.ECID, map[string]any{"TestRestore": true},
		func(context.Context, uint16) (net.Conn, error) { return nil, fmt.Errorf("unexpected dial") },
		SessionHandlers{Data: data.Handle, Event: func(context.Context, map[string]any) error { events++; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("events=%d", events)
	}
	if _, err := host.Write([]byte{0}); err == nil {
		t.Fatal("control connection left open")
	}
}

func TestRestoredSessionFailure(t *testing.T) {
	for _, tt := range []struct {
		name                           string
		message                        map[string]any
		wrongECID, eof, handlerFailure bool
		want                           string
	}{
		{name: "wrong ECID", wrongECID: true, want: "ECID"},
		{name: "EOF", eof: true, want: "receive"},
		{name: "failed status", message: map[string]any{"MsgType": "StatusMsg", "Status": int64(14)}, want: "status 14"},
		{name: "missing status", message: map[string]any{"MsgType": "StatusMsg"}, want: "invalid restored status"},
		{name: "unknown message", message: map[string]any{"MsgType": "FutureMessage"}, want: "unsupported"},
		{name: "crash", message: map[string]any{"MsgType": "RestoredCrash"}, want: "crash"},
		{name: "baseband rejected", message: map[string]any{"MsgType": "BBUpdateStatusMsg", "Accepted": false}, want: "not accepted"},
		{name: "invalid port", message: map[string]any{"MsgType": "DataRequestMsg", "DataType": "RootTicket", "DataPort": uint64(65536)}, want: "data port"},
		{name: "async control", message: map[string]any{"MsgType": "AsyncDataRequestMsg", "DataType": "RootTicket"}, want: "dedicated"},
		{name: "handler failed", message: map[string]any{"MsgType": "DataRequestMsg", "DataType": "RootTicket"}, handlerFailure: true, want: "injected"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host, peer := net.Pipe()
			defer peer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				ecid := uint64(17)
				if tt.wrongECID {
					ecid++
				}
				if err := sessionPeer(peer, ecid); err != nil {
					done <- err
					return
				}
				if tt.wrongECID {
					_, err := iosrestore.Receive(peer)
					if err == nil {
						done <- fmt.Errorf("started on wrong device")
					} else {
						done <- nil
					}
					return
				}
				if err := expectStart(peer); err != nil {
					done <- err
					return
				}
				if tt.eof {
					peer.Close()
					done <- nil
					return
				}
				if err := iosrestore.Send(peer, tt.message); err != nil {
					done <- err
					return
				}
				_, err := iosrestore.Receive(peer)
				if err == nil {
					done <- fmt.Errorf("acknowledged failure")
				} else {
					done <- nil
				}
			}()
			err := RunRestored(ctx, host, 17, map[string]any{"TestRestore": true}, func(context.Context, uint16) (net.Conn, error) { return nil, fmt.Errorf("unexpected dial") }, SessionHandlers{Data: func(context.Context, net.Conn, map[string]any) error {
				if tt.handlerFailure {
					return fmt.Errorf("injected failure")
				}
				return nil
			}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRestoredAsyncFailure(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		if err := sessionPeer(peer, 17); err != nil {
			done <- err
			return
		}
		if err := expectStart(peer); err != nil {
			done <- err
			return
		}
		done <- iosrestore.Send(peer, map[string]any{"MsgType": "AsyncDataRequestMsg", "DataType": "RootTicket", "DataPort": int64(62000)})
	}()
	service, remote := net.Pipe()
	defer remote.Close()
	failure := errors.New("asynchronous injected failure")
	err := RunRestored(ctx, host, 17, map[string]any{"TestRestore": true}, func(_ context.Context, port uint16) (net.Conn, error) {
		if port != 62000 {
			return nil, fmt.Errorf("incorrect port %d", port)
		}
		return service, nil
	}, SessionHandlers{Data: func(context.Context, net.Conn, map[string]any) error { return failure }})
	if !errors.Is(err, failure) {
		t.Fatalf("async error lost: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Write([]byte{0}); err == nil {
		t.Fatal("data connection left open")
	}
}

func TestRestoreDataPort(t *testing.T) {
	for _, tt := range []struct {
		name, kind string
		value      any
		want       uint16
		bad        bool
	}{
		{name: "control", kind: "RootTicket"},
		{name: "system ASR", kind: "SystemImageData", want: 12345},
		{name: "recovery ASR", kind: "RecoveryOSASRImage", want: 12345},
		{name: "explicit ASR", kind: "SystemImageData", value: int64(62000), want: 62000},
		{name: "zero", kind: "RootTicket", value: int64(0), bad: true},
		{name: "negative", kind: "RootTicket", value: int64(-1), bad: true},
		{name: "string", kind: "RootTicket", value: "0x1234", bad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{}
			if tt.value != nil {
				m["DataPort"] = tt.value
			}
			got, err := restoreDataPort(m, tt.kind)
			if got != tt.want || (err != nil) != tt.bad {
				t.Fatalf("got %d, %v", got, err)
			}
		})
	}
}

func ExampleRunRestored() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RunRestored(ctx, nil, 0, nil, nil, SessionHandlers{})
	fmt.Println(err)
	// Output: restored control connection is required
}

func TestRestoredAsyncCompletion(t *testing.T) {
	build, device := signingFixture()
	_, want, err := APSigningRequest(build, device, []string{"iBSS"})
	if err != nil {
		t.Fatal(err)
	}
	data := &SessionData{RecoveryTicket: nextPolicyTicket(device), RecoveryRequirements: want}
	host, peer := net.Pipe()
	defer peer.Close()
	service, remote := net.Pipe()
	defer remote.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ready := make(chan struct{})
	completed := make(chan struct{})
	controlDone := make(chan error, 1)
	dataDone := make(chan error, 1)
	go func() {
		remote.SetDeadline(time.Now().Add(3 * time.Second))
		response, err := iosrestore.Receive(remote)
		if err == nil && response["RootTicketData"] == nil {
			err = fmt.Errorf("missing asynchronous ticket")
		}
		dataDone <- err
	}()
	go func() {
		if err := sessionPeer(peer, device.ECID); err != nil {
			controlDone <- err
			return
		}
		if err := expectStart(peer); err != nil {
			controlDone <- err
			return
		}
		for _, m := range []map[string]any{
			{"MsgType": "AsyncDataRequestMsg", "DataType": "RecoveryOSRootTicketData", "DataPort": int64(62001)},
			{"MsgType": "ProgressMsg", "Progress": int64(99)},
			{"MsgType": "StatusMsg", "Status": int64(0)},
		} {
			if err := iosrestore.Send(peer, m); err != nil {
				controlDone <- err
				return
			}
		}
		ack, err := iosrestore.Receive(peer)
		if err == nil && ack["MsgType"] != "ReceivedFinalStatusMsg" {
			err = fmt.Errorf("missing acknowledgement")
		}
		select {
		case <-completed:
		default:
			err = fmt.Errorf("acknowledged before data handler finished")
		}
		controlDone <- err
	}()
	err = RunRestored(ctx, host, device.ECID, map[string]any{"TestRestore": true}, func(_ context.Context, port uint16) (net.Conn, error) {
		if port != 62001 {
			return nil, fmt.Errorf("wrong port")
		}
		return service, nil
	}, SessionHandlers{
		Data: func(ctx context.Context, conn net.Conn, m map[string]any) error {
			select {
			case <-ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			err := data.Handle(ctx, conn, m)
			close(completed)
			return err
		}, Event: func(context.Context, map[string]any) error { close(ready); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-controlDone; err != nil {
		t.Fatal(err)
	}
	if err := <-dataDone; err != nil {
		t.Fatal(err)
	}
}

func TestRestoredCancellation(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan error, 1)
	go func() {
		if err := sessionPeer(peer, 17); err != nil {
			started <- err
			return
		}
		started <- expectStart(peer)
	}()
	done := make(chan error, 1)
	go func() {
		done <- RunRestored(ctx, host, 17, map[string]any{"TestRestore": true}, func(context.Context, uint16) (net.Conn, error) { return nil, fmt.Errorf("unexpected dial") }, SessionHandlers{Data: func(context.Context, net.Conn, map[string]any) error { return nil }})
	}()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not interrupt control read")
	}
}

func ExampleSessionHandlers() {
	handlers := SessionHandlers{Data: (&SessionData{}).Handle}
	fmt.Println(handlers.Data != nil)
	// Output: true
}
