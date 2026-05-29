package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/buildscratch"
	controlx "github.com/tmc/cove/internal/control"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestWithBuildRuntimeGlobalsSetsAndRestores(t *testing.T) {
	oldVMDir := vmDir
	oldDiskPath := diskPath
	oldLinuxMode := linuxMode
	oldWindowsMode := windowsMode
	oldGUIMode := guiMode
	oldHeadlessMode := headlessMode
	oldSkipResume := skipResume
	oldRecoveryMode := recoveryMode
	oldBootArgs := bootArgs
	oldRunHTTPAddr := runHTTPAddr
	oldAutoMountVolumes := autoMountVolumes
	oldSerialOutput := serialOutput
	t.Cleanup(func() {
		vmDir = oldVMDir
		diskPath = oldDiskPath
		linuxMode = oldLinuxMode
		windowsMode = oldWindowsMode
		guiMode = oldGUIMode
		headlessMode = oldHeadlessMode
		skipResume = oldSkipResume
		recoveryMode = oldRecoveryMode
		bootArgs = oldBootArgs
		runHTTPAddr = oldRunHTTPAddr
		autoMountVolumes = oldAutoMountVolumes
		serialOutput = oldSerialOutput
	})

	vmDir = "old-vm"
	diskPath = "old-disk"
	linuxMode = false
	windowsMode = false
	guiMode = true
	headlessMode = false
	skipResume = false
	recoveryMode = true
	bootArgs = "debug"
	runHTTPAddr = ":0"
	autoMountVolumes = true
	serialOutput = "stdout"

	sc := buildscratch.Scratch{Dir: filepath.Join(t.TempDir(), "scratch"), DiskPath: filepath.Join(t.TempDir(), "scratch", "linux-disk.img")}
	err := withBuildRuntimeGlobals(sc, func() error {
		if vmDir != sc.Dir || diskPath != sc.DiskPath {
			return fmt.Errorf("paths = %q/%q, want %q/%q", vmDir, diskPath, sc.Dir, sc.DiskPath)
		}
		if !linuxMode || windowsMode || guiMode || !headlessMode || !skipResume || recoveryMode || bootArgs != "" || runHTTPAddr != "" || autoMountVolumes || serialOutput != "none" {
			return fmt.Errorf("unexpected runtime globals")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if vmDir != "old-vm" || diskPath != "old-disk" || linuxMode || windowsMode || !guiMode || headlessMode || skipResume || !recoveryMode || bootArgs != "debug" || runHTTPAddr != ":0" || !autoMountVolumes || serialOutput != "stdout" {
		t.Fatalf("runtime globals were not restored")
	}
}

func TestWithBuildRuntimeGlobalsRestoresAfterError(t *testing.T) {
	oldVMDir := vmDir
	t.Cleanup(func() { vmDir = oldVMDir })
	vmDir = "old-vm"
	wantErr := errors.New("failed")
	sc := buildscratch.Scratch{Dir: filepath.Join(t.TempDir(), "scratch"), DiskPath: filepath.Join(t.TempDir(), "scratch", "disk.img")}
	err := withBuildRuntimeGlobals(sc, func() error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("withBuildRuntimeGlobals() = %v, want %v", err, wantErr)
	}
	if vmDir != "old-vm" {
		t.Fatalf("vmDir = %q, want restored old-vm", vmDir)
	}
}

func TestWithBuildRuntimeGlobalsRejectsIncompleteScratch(t *testing.T) {
	if err := withBuildRuntimeGlobals(buildscratch.Scratch{}, func() error { return nil }); err == nil {
		t.Fatal("withBuildRuntimeGlobals() error = nil, want missing dir")
	}
	if err := withBuildRuntimeGlobals(buildscratch.Scratch{Dir: "scratch"}, func() error { return nil }); err == nil {
		t.Fatal("withBuildRuntimeGlobals() error = nil, want missing disk")
	}
}

func TestStartBuildGuestUsesDefaultStarter(t *testing.T) {
	old := defaultBuildGuestStart
	defer func() { defaultBuildGuestStart = old }()

	var got buildscratch.Scratch
	defaultBuildGuestStart = func(ctx context.Context, sc buildscratch.Scratch) (buildGuestCleanup, error) {
		got = sc
		return func(context.Context) error { return nil }, nil
	}
	exec := testBuildExecutor(t.TempDir())
	sc := buildscratch.Scratch{Dir: "scratch", DiskPath: "disk.img"}
	cleanup, err := exec.startBuildGuest(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil {
		t.Fatal("cleanup = nil")
	}
	if got != sc {
		t.Fatalf("scratch = %#v, want %#v", got, sc)
	}
}

func TestWaitBuildAgentRetriesUntilSuccess(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		if sock != "sock" || req.Type != "agent-ping" || cmdType != "agent-ping" {
			t.Fatalf("request = sock %q type %q cmd %q", sock, req.Type, cmdType)
		}
		if *call == 1 {
			return &controlpb.ControlResponse{Success: false, Error: "booting"}, nil
		}
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	if err := waitBuildAgent(context.Background(), "sock", time.Second); err != nil {
		t.Fatalf("waitBuildAgent(): %v", err)
	}
}

func TestWaitBuildAgentRetriesUntilSocketReady(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "cove-build-agent-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "control.sock")
	if err := os.WriteFile(filepath.Join(dir, controlTokenFileName), []byte("token\n"), 0600); err != nil {
		t.Fatal(err)
	}

	started := make(chan *controlx.Server, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		s := &controlx.Server{
			SocketPath:  sock,
			Handler:     buildAgentPingHandler{},
			StopTimeout: 100 * time.Millisecond,
		}
		if err := s.Start(context.Background()); err != nil {
			t.Errorf("Start: %v", err)
			return
		}
		started <- s
	}()

	if err := waitBuildAgent(context.Background(), sock, 2*time.Second); err != nil {
		t.Fatalf("waitBuildAgent(): %v", err)
	}
	select {
	case s := <-started:
		s.Stop()
	case <-time.After(time.Second):
		t.Fatal("server did not report start")
	}
}

func TestWaitBuildAgentHonorsContext(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		return nil, errors.New("unreachable")
	})
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitBuildAgent(ctx, "sock", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBuildAgent() = %v, want context.Canceled", err)
	}
}

type buildAgentPingHandler struct{}

func (buildAgentPingHandler) Authorize(token string) bool { return token == "token" }

func (buildAgentPingHandler) HandleStream(net.Conn, *controlpb.ControlRequest, []byte) (bool, bool) {
	return false, false
}

func (buildAgentPingHandler) HandleRaw(*controlpb.ControlRequest, []byte) (*controlpb.ControlResponse, bool) {
	return nil, false
}

func (buildAgentPingHandler) Handle(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	if req.Type != "agent-ping" {
		return &controlpb.ControlResponse{Error: "unexpected request"}
	}
	return &controlpb.ControlResponse{Success: true}
}

func (buildAgentPingHandler) Event(string, *controlpb.ControlResponse) {}

func TestShutdownBuildGuest(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		if sock != "sock" || req.Type != "agent-shutdown" || req.GetAgentShutdown() == nil || cmdType != "agent-shutdown" {
			t.Fatalf("request = sock %q type %q cmd %q", sock, req.Type, cmdType)
		}
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	if err := shutdownBuildGuest(context.Background(), "sock"); err != nil {
		t.Fatalf("shutdownBuildGuest(): %v", err)
	}
}

func TestShutdownBuildGuestReportsFailure(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		return &controlpb.ControlResponse{Success: false, Error: "denied"}, nil
	})
	defer restore()
	err := shutdownBuildGuest(context.Background(), "sock")
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("shutdownBuildGuest() = %v, want denial", err)
	}
}

func TestCompactBuildScratchTargetedLinux(t *testing.T) {
	root := t.TempDir()
	sc := buildscratch.Scratch{Dir: filepath.Join(root, "scratch")}
	if err := os.MkdirAll(sc.Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sc.Dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	var got string
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		if cmdType != "agent-exec" {
			t.Fatalf("cmdType = %q, want agent-exec", cmdType)
		}
		got = strings.Join(req.GetAgentExec().GetArgs(), " ")
		return &controlpb.ControlResponse{
			Success: true,
			Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{}},
		}, nil
	})
	defer restore()
	if err := compactBuildScratch(context.Background(), sc, "targeted"); err != nil {
		t.Fatalf("compactBuildScratch(targeted): %v", err)
	}
	for _, want := range []string{"/var/log/*", "/var/cache/*", "/tmp/*", "sync"} {
		if !strings.Contains(got, want) {
			t.Fatalf("targeted compact script missing %q: %s", want, got)
		}
	}
}

func TestTargetedBuildCompactScript(t *testing.T) {
	tests := []struct {
		platform string
		want     string
		wantErr  string
	}{
		{platform: "linux", want: "/var/cache/*"},
		{platform: "macos", want: "/var/db/diagnostics/*"},
		{platform: "windows", wantErr: "unsupported guest platform"},
	}
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			got, err := targetedBuildCompactScript(tt.platform)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("targetedBuildCompactScript() = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("targetedBuildCompactScript(): %v", err)
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("targetedBuildCompactScript() = %q, want %q", got, tt.want)
			}
		})
	}
}

func stubBuildControlSender(t *testing.T, fn func(*int, string, *controlpb.ControlRequest, time.Duration, string) (*controlpb.ControlResponse, error)) func() {
	t.Helper()
	old := sendBuildControlRequest
	calls := 0
	sendBuildControlRequest = func(sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return fn(&calls, sock, req, timeout, cmdType)
	}
	return func() {
		sendBuildControlRequest = old
	}
}
