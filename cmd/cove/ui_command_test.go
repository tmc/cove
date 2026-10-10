package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/guest"
	"github.com/tmc/cove/internal/vmconfig"
	pb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestUIStatusCommandPropagatesReadBounds(t *testing.T) {
	parent := filepath.Join(os.Getenv("HOME"), "tmp", "vz-macos", time.Now().Format("20060102")+"-ui-tests")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(parent, "cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv(vmconfig.StateDirEnv, root)
	dir := filepath.Join(root, "vms", "ui")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range vmconfig.RequiredFiles {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	socket := GetControlSocketPathForVM(dir)
	if filepath.Dir(socket) != dir {
		t.Fatal("UI fixture socket escaped scoped directory")
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requests := make(chan *controlpb.ControlRequest, 5)
	serverDone := make(chan error, 1)
	go func() {
		for _, state := range []string{"ready", "locked", "permission_denied", "unsupported_protocol", "stale_generation"} {
			conn, err := listener.Accept()
			if err != nil {
				serverDone <- err
				return
			}
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			data, err := bufio.NewReader(conn).ReadBytes('\n')
			if err != nil {
				conn.Close()
				serverDone <- err
				return
			}
			request := new(controlpb.ControlRequest)
			if err := protojsonUnmarshaler.Unmarshal(data, request); err != nil {
				conn.Close()
				serverDone <- err
				return
			}
			requests <- request
			response := &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentUi{AgentUi: &pb.UIResponse{Status: &pb.UIStatus{State: state, Generation: "current", PermissionState: "unknown", SessionState: "locked"}}}}
			reply, err := protojsonMarshaler.Marshal(response)
			if err == nil {
				_, err = conn.Write(append(reply, '\n'))
			}
			conn.Close()
			if err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	for _, state := range []string{"ready", "locked", "permission_denied", "unsupported_protocol", "stale_generation"} {
		var out, stderr bytes.Buffer
		err := handleUICommand(commandEnv{Stdout: &out, Stderr: &stderr}, []string{"status", "-vm", "ui", "-timeout", "350ms", "-generation", "previous", "-json"})
		if err != nil {
			t.Fatalf("status %s: %v stderr=%s", state, err, stderr.String())
		}
		var result guest.Observation
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status.State != state || result.Status.Generation != "current" {
			t.Fatalf("status lost: %+v", result.Status)
		}
		select {
		case request := <-requests:
			if request.Type != "agent-ui-status" || request.GetAgentUi().ExpectedGeneration != "previous" || request.GetAgentUi().TimeoutMs != 350 {
				t.Fatalf("read bounds lost or fallback request: %v", request)
			}
		case <-time.After(time.Second):
			t.Fatal("status request absent")
		}
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake UI transport did not finish")
	}
}
