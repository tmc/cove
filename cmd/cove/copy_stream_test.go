package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	control "github.com/tmc/cove/internal/control"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestCopyStreamDisconnectCancelsBackend(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	cancelled := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer server.Close()
		serveCopyStream(context.Background(), server, func(ctx context.Context) *controlpb.ControlResponse {
			<-ctx.Done()
			close(cancelled)
			return &controlpb.ControlResponse{Error: ctx.Err().Error()}
		})
	}()
	if _, err := bufio.NewReader(client).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	client.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("copy RPC did not receive cancellation")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
}

func TestCopyStreamProgressAndFinalResponse(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		serveCopyStream(context.Background(), server, func(context.Context) *controlpb.ControlResponse {
			return &controlpb.ControlResponse{Success: true, Data: "copied file"}
		})
	}()
	reader := bufio.NewReader(client)
	for i := 0; i < 2; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var resp controlpb.ControlResponse
		if err := control.ProtoJSONUnmarshaler.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatal(err)
		}
		if !resp.Success {
			t.Fatalf("response failed: %s", resp.Error)
		}
		if i == 0 {
			var frame map[string]string
			if err := json.Unmarshal([]byte(resp.Data), &frame); err != nil {
				t.Fatal(err)
			}
			if frame["copyProgress"] == "" {
				t.Fatal("missing progress")
			}
		} else if resp.Data != "copied file" {
			t.Fatalf("final data = %q", resp.Data)
		}
	}
}

func TestCopyDropDestination(t *testing.T) {
	for _, tt := range []struct{ host, want string }{{"/Users/tmc/report.txt", "~/Downloads/Cove-123/report.txt"}, {"/Users/tmc/My Files/", "~/Downloads/Cove-123/My Files"}, {"/Users/tmc/a'b.txt", "~/Downloads/Cove-123/a'b.txt"}} {
		got := copyDropDestinations("Cove-123", []string{tt.host})[0]
		if got != tt.want {
			t.Errorf("%q = %q, want %q", tt.host, got, tt.want)
		}
		if !strings.HasPrefix(got, "~/Downloads/") {
			t.Fatal("destination outside Downloads")
		}
	}
}

func TestCopyDropDuplicateNames(t *testing.T) {
	paths := []string{"/one/a.txt", "/two/a.txt", "/three/a (2).txt", "/four/a.txt"}
	got := copyDropDestinations("Cove-123", paths)
	want := []string{"~/Downloads/Cove-123/a.txt", "~/Downloads/Cove-123/a (2).txt", "~/Downloads/Cove-123/a (2) (2).txt", "~/Downloads/Cove-123/a (3).txt"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("destination %d = %q, want %q", i, got[i], want[i])
		}
	}
}
