package restore

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tmc/apple/x/iosrestore"
	"github.com/tmc/apple/x/plist"
)

func TestSessionDataResponses(t *testing.T) {
	for _, kind := range []string{"RootTicket", "RecoveryOSRootTicketData", "BuildIdentityDict", "RecoveryOSLocalPolicy"} {
		t.Run(kind, func(t *testing.T) {
			build, device := signingFixture()
			_, want, err := APSigningRequest(build, device, []string{"iBSS"})
			if err != nil {
				t.Fatal(err)
			}
			data := &SessionData{APResponse: map[string]any{"ApImg4Ticket": nextPolicyTicket(device)}, RecoveryResponse: map[string]any{"ApImg4Ticket": nextPolicyTicket(device)}, APRequirements: want, RecoveryRequirements: want, Device: device, Client: localPolicyServer(t, false), Identity: func(context.Context, map[string]any) (map[string]any, error) { return build, nil }}
			message := map[string]any{"DataType": kind, "Arguments": volumePolicyArguments()}
			message["Arguments"].(map[string]any)["Variant"] = "Upgrade"
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			host.SetDeadline(time.Now().Add(3 * time.Second))
			peer.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() { done <- data.Handle(ctx, host, message) }()
			response, err := iosrestore.Receive(peer)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "RootTicket", "RecoveryOSRootTicketData":
				ticket, ok := response["RootTicketData"].([]byte)
				if !ok || !bytes.Equal(ticket, nextPolicyTicket(device)) {
					t.Fatal("wrong ticket response")
				}
			case "BuildIdentityDict":
				if response["BuildIdentityDict"] == nil || response["Variant"] != "Upgrade" {
					t.Fatal("missing identity or variant")
				}
			case "RecoveryOSLocalPolicy":
				if _, ok := response["Ap,LocalPolicy"].([]byte); !ok {
					t.Fatal("missing volume policy")
				}
			}
		})
	}
}

func TestSessionDataASR(t *testing.T) {
	for _, kind := range []string{"SystemImageData", "RecoveryOSASRImage"} {
		t.Run(kind, func(t *testing.T) {
			payload := []byte("test filesystem bytes")
			image := RestoreImage{Reader: bytes.NewReader(payload), Size: int64(len(payload))}
			data := &SessionData{SystemImage: image, RecoveryImage: image}
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			peer.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() { done <- data.Handle(ctx, host, map[string]any{"DataType": kind}) }()
			if _, err := io.WriteString(peer, "<plist version=\"1.0\"><dict><key>Command</key><string>Initiate</string></dict></plist>\n"); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(peer)
			var info bytes.Buffer
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				info.WriteString(line)
				if strings.Contains(line, "</plist>") {
					break
				}
			}
			value, err := plist.ParseBytes(info.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if value.(map[string]any)["Payload"].(map[string]any)["Size"] != int64(len(payload)) {
				t.Fatal("wrong ASR size advertisement")
			}
			if _, err := io.WriteString(peer, "<plist version=\"1.0\"><dict><key>Command</key><string>Payload</string></dict></plist>\n"); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(reader, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("wrong ASR image")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionDataReject(t *testing.T) {
	for _, kind := range []string{"RootTicket", "RecoveryOSRootTicketData", "SystemImageData", "RecoveryOSASRImage", "BuildIdentityDict", "RecoveryOSLocalPolicy", "Unsupported"} {
		t.Run(kind, func(t *testing.T) {
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			if err := (&SessionData{}).Handle(context.Background(), host, map[string]any{"DataType": kind}); err == nil {
				t.Fatal("accepted missing restore data")
			}
		})
	}
}

func ExampleRestoreImage() {
	image := RestoreImage{Reader: bytes.NewReader([]byte("image")), Size: 5}
	fmt.Println(image.Size)
	// Output: 5
}

func ExampleSessionData() {
	data := SessionData{SystemImage: RestoreImage{Reader: bytes.NewReader([]byte("image")), Size: 5}}
	fmt.Println(data.SystemImage.Size)
	// Output: 5
}

func ExampleSessionData_Handle() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&SessionData{}).Handle(ctx, nil, nil)
	fmt.Println(err)
	// Output: context canceled
}
