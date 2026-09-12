package main

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestWindowsShutdownCommand(t *testing.T) {
	for _, tt := range []struct {
		name          string
		reboot, force bool
		want          []string
	}{
		{"shutdown", false, false, []string{"shutdown.exe", "/s", "/t", "0"}},
		{"forced shutdown", false, true, []string{"shutdown.exe", "/s", "/t", "0", "/f"}},
		{"reboot", true, false, []string{"shutdown.exe", "/r", "/t", "0"}},
		{"forced reboot", true, true, []string{"shutdown.exe", "/r", "/t", "0", "/f"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := shutdownCommand(tt.reboot, tt.force).Args; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("args = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWindowsFilesystem(t *testing.T) {
	for _, path := range []string{t.TempDir(), "/"} {
		total, available, err := statFilesystem(path)
		if err != nil {
			t.Fatalf("statFilesystem(%q): %v", path, err)
		}
		if total == 0 || available > total {
			t.Fatalf("path = %q, total = %d, available = %d", path, total, available)
		}
	}
	if _, _, err := statFilesystem("\x00"); err == nil {
		t.Fatal("NUL path accepted")
	}
}

func TestWindowsSignalExec(t *testing.T) {
	if os.Getenv("COVE_SIGNAL_TEST_CHILD") == "1" {
		time.Sleep(time.Minute)
		return
	}
	for _, sig := range []int32{0, 2, 15, -1} {
		if allowedExecSignal(sig) {
			t.Fatalf("signal %d accepted", sig)
		}
		if err := signalExec(os.Getpid(), sig); err == nil {
			t.Fatalf("signal %d succeeded", sig)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsSignalExec$")
	cmd.Env = append(os.Environ(), "COVE_SIGNAL_TEST_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := signalExec(cmd.Process.Pid, 9); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("terminated process succeeded")
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
}
