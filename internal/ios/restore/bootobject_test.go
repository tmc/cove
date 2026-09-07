package restore

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/tmc/apple/x/iosrestore"
)

type objectReader struct {
	io.Reader
	closed bool
}

func (r *objectReader) Close() error { r.closed = true; return nil }

func TestBootObjectStreaming(t *testing.T) {
	for _, tt := range []struct {
		name, kind, image string
		personalized, aea bool
	}{
		{name: "V3 personalized", kind: "PersonalizedBootObjectV3", image: "iBSS", personalized: true},
		{name: "V3 global manifest", kind: "PersonalizedBootObjectV3", image: "__GlobalManifest__"},
		{name: "V3 restore version", kind: "PersonalizedBootObjectV3", image: "__RestoreVersion__"},
		{name: "V3 system version", kind: "PersonalizedBootObjectV3", image: "__SystemVersion__"},
		{name: "V4 raw", kind: "SourceBootObjectV4", image: "OS"},
		{name: "V4 AEA", kind: "SourceBootObjectV4", image: "OS", aea: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			build, device := signingFixture()
			payload := bytes.Repeat([]byte{0x42}, 2*8192+17)
			if tt.personalized {
				payload, _ = hex.DecodeString(syntheticPayload)
			}
			if tt.aea {
				copy(payload, "AEA1")
			}
			source := &objectReader{Reader: bytes.NewReader(payload)}
			response := map[string]any{"ApImg4Ticket": nextPolicyTicket(device), "iBSS-TBM": map[string]any{"BNCN": []byte{1, 2, 3, 4, 5, 6, 7, 8}}}
			assetCalled := false
			data := &SessionData{APResponse: response, Device: device,
				Identity: func(context.Context, map[string]any) (map[string]any, error) { return build, nil },
				Object: func(_ context.Context, _ map[string]any, name string) (io.ReadCloser, error) {
					if name != tt.image {
						return nil, fmt.Errorf("wrong object name %s", name)
					}
					return source, nil
				},
				NestedAsset: func(_ context.Context, m map[string]any) error {
					if m["MsgType"] != "URLAsset" || m["DataPort"] != int64(62002) {
						return fmt.Errorf("incorrect asset request")
					}
					assetCalled = true
					return nil
				},
			}
			want := payload
			if tt.personalized {
				var err error
				want, err = Personalize("iBSS", payload, response, build["Info"].(map[string]any), nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			host.SetDeadline(time.Now().Add(3 * time.Second))
			peer.SetDeadline(time.Now().Add(3 * time.Second))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- data.Handle(ctx, host, map[string]any{"DataType": tt.kind, "Arguments": map[string]any{"ImageName": tt.image}})
			}()
			var got []byte
			chunks := 0
			for {
				message, err := iosrestore.Receive(peer)
				if err != nil {
					t.Fatal(err)
				}
				if message["FileDataDone"] == true {
					if len(message) != 1 {
						t.Fatal("extra final fields")
					}
					break
				}
				chunk, ok := message["FileData"].([]byte)
				if !ok || len(chunk) == 0 || len(chunk) > 8192 || len(message) != 1 {
					t.Fatal("invalid boot object chunk")
				}
				got = append(got, chunk...)
				chunks++
				if tt.aea && chunks == 1 {
					if err := iosrestore.Send(peer, map[string]any{"MsgType": "URLAsset", "DataPort": int64(62002)}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) || chunks != (len(want)+8191)/8192 {
				t.Fatal("incorrect chunked object")
			}
			if !source.closed || assetCalled != tt.aea {
				t.Fatal("incorrect object ownership or asset handshake")
			}
		})
	}
}

func TestBootObjectReject(t *testing.T) {
	for _, name := range []string{"missing image", "missing source", "stale ticket", "asset failure", "source failure"} {
		t.Run(name, func(t *testing.T) {
			build, device := signingFixture()
			payload, _ := hex.DecodeString(syntheticPayload)
			reader := &objectReader{Reader: bytes.NewReader(payload)}
			data := &SessionData{Device: device, APResponse: map[string]any{"ApImg4Ticket": nextPolicyTicket(device)}, Identity: func(context.Context, map[string]any) (map[string]any, error) { return build, nil }, Object: func(context.Context, map[string]any, string) (io.ReadCloser, error) { return reader, nil }}
			message := map[string]any{"DataType": "PersonalizedBootObjectV3", "Arguments": map[string]any{"ImageName": "iBSS"}}
			switch name {
			case "missing image":
				message["Arguments"] = map[string]any{}
			case "missing source":
				data.Object = nil
			case "stale ticket":
				data.Device.APNonce = []byte{99}
			case "source failure":
				data.Object = func(context.Context, map[string]any, string) (io.ReadCloser, error) {
					return nil, fmt.Errorf("injected source failure")
				}
			case "asset failure":
				message["DataType"] = "SourceBootObjectV4"
				reader.Reader = bytes.NewReader([]byte("AEA1source"))
				data.NestedAsset = func(context.Context, map[string]any) error { return fmt.Errorf("injected asset failure") }
			}
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			host.SetDeadline(time.Now().Add(time.Second))
			peer.SetDeadline(time.Now().Add(time.Second))
			done := make(chan error, 1)
			if name == "asset failure" {
				go func() {
					if _, err := iosrestore.Receive(peer); err != nil {
						done <- err
						return
					}
					done <- iosrestore.Send(peer, map[string]any{"MsgType": "URLAsset"})
				}()
			}
			if err := data.Handle(context.Background(), host, message); err == nil {
				t.Fatal("accepted invalid boot object")
			}
			if name == "asset failure" {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			if (name == "stale ticket" || name == "asset failure") && !reader.closed {
				t.Fatal("source not closed on failure")
			}
		})
	}
}

func TestBootObjectAssetRead(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		partial, wrong, canceled bool
		ok                       bool
	}{
		{name: "no request", ok: true},
		{name: "partial frame", partial: true},
		{name: "wrong message", wrong: true},
		{name: "canceled", canceled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			sent := make(chan error, 1)
			if tt.partial {
				go func() { _, err := peer.Write([]byte{0, 0}); sent <- err }()
			}
			if tt.wrong {
				go func() { sent <- iosrestore.Send(peer, map[string]any{"MsgType": "ProgressMsg"}) }()
			}
			err := bootObjectAsset(ctx, host, nil, 10*time.Millisecond)
			if (err == nil) != tt.ok {
				t.Fatalf("got %v", err)
			}
			if tt.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v", err)
			}
			if tt.partial || tt.wrong {
				if err := <-sent; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestLegacyBootObject(t *testing.T) {
	for _, tt := range []struct{ kind, name string }{
		{"KernelCache", "KernelCache"}, {"DeviceTree", "DeviceTree"}, {"SystemImageRootHash", "SystemVolume"}, {"SystemImageCanonicalMetadata", "Ap,SystemVolumeCanonicalMetadata"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			build, device := signingFixture()
			manifest := build["Manifest"].(map[string]any)
			manifest[tt.name] = map[string]any{"Trusted": true, "Digest": []byte{4, 5, 6}, "Info": map[string]any{"Path": "object.im4p"}}
			props := map[string]any{"ECID": device.ECID, "BORD": device.BoardID, "CHIP": device.ChipID, "BNCH": device.APNonce, "snon": device.SEPNonce, "SDOM": uint64(1), "CPRO": device.ProductionMode, "CSEC": device.SecurityMode}
			payload, _ := hex.DecodeString(syntheticPayload)
			data := &SessionData{Device: device, APResponse: map[string]any{"ApImg4Ticket": testTicket(props, map[string][]byte{componentFourCC[tt.name]: {4, 5, 6}})}, Identity: func(context.Context, map[string]any) (map[string]any, error) { return build, nil }, Object: func(_ context.Context, _ map[string]any, name string) (io.ReadCloser, error) {
				if name != tt.name {
					return nil, fmt.Errorf("wrong legacy component")
				}
				return io.NopCloser(bytes.NewReader(payload)), nil
			}}
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			host.SetDeadline(time.Now().Add(time.Second))
			peer.SetDeadline(time.Now().Add(time.Second))
			done := make(chan error, 1)
			go func() { done <- data.Handle(context.Background(), host, map[string]any{"DataType": tt.kind}) }()
			response, err := iosrestore.Receive(peer)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := response[tt.kind+"File"].([]byte); !ok || len(response) != 1 {
				t.Fatal("incorrect legacy response")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
