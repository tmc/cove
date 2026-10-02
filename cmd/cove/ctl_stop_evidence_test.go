package main

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCtlStopStatusErrorsAreNotStopEvidence(t *testing.T) {
	old := ctlShutdownPollInterval
	ctlShutdownPollInterval = time.Millisecond
	t.Cleanup(func() { ctlShutdownPollInterval = old })
	failure := errors.New("status read timeout")
	for _, tt := range []struct {
		name        string
		states      []string
		errors      []error
		wantSuccess bool
	}{
		{"error-running", []string{"", "running"}, []error{failure, nil}, false},
		{"error-stopped", []string{"", "stopped"}, []error{failure, nil}, true},
		{"malformed-status", []string{""}, []error{errors.New("parse status: invalid JSON")}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			out, err := captureStdoutResult(t, func() error {
				return ctlWaitForVMStoppedWithStatus("unused", 200*time.Millisecond, func(timeout time.Duration, deadline time.Time) (string, error) {
					index := calls
					if index >= len(tt.states) {
						index = len(tt.states) - 1
					}
					calls++
					return tt.states[index], tt.errors[index]
				})
			})
			if (err == nil) != tt.wantSuccess {
				t.Fatalf("error=%v", err)
			}
			if !tt.wantSuccess && strings.Contains(out, "VM stopped") {
				t.Fatalf("false stop claim: %s", out)
			}
			if calls < 2 {
				t.Fatal("transient error was not retried")
			}
			if tt.name == "malformed-status" && (!strings.Contains(err.Error(), "stop not verified") || !errors.Is(err, tt.errors[0])) {
				t.Fatalf("missing last error: %v", err)
			}
		})
	}
}

func TestCtlStopMissingSocketDoesNotSucceed(t *testing.T) {
	err := ctlWaitForVMStopped("/nonexistent-cove-control.sock", 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "stop not verified") {
		t.Fatalf("error=%v", err)
	}
}

func TestCtlStopWaitBoundsPollByRemainingDeadline(t *testing.T) {
	wait := 10 * time.Millisecond
	started := time.Now()
	err := ctlWaitForVMStoppedWithStatus("unused", wait, func(timeout time.Duration, deadline time.Time) (string, error) {
		if timeout <= 0 || timeout > wait {
			t.Fatalf("poll exceeds budget: %s", timeout)
		}
		time.Sleep(timeout)
		return "", errors.New("status timeout")
	})
	if err == nil || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("wait error=%v elapsed=%s", err, time.Since(started))
	}
}

func TestCtlStopExpiredStatusDeadline(t *testing.T) {
	_, err := ctlVMStatusStateUntil("missing.sock", time.Second, time.Now().Add(-time.Second))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired status error=%v", err)
	}
}

func TestCtlStopLateStoppedObservationRejected(t *testing.T) {
	err := ctlWaitForVMStoppedWithStatus("unused", 5*time.Millisecond, func(timeout time.Duration, deadline time.Time) (string, error) {
		time.Sleep(10 * time.Millisecond)
		return "stopped", nil
	})
	if err == nil || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("accepted late stopped observation: %v", err)
	}
}

func TestCtlStopStatusTransportUsesOriginalDeadline(t *testing.T) {
	dir := shortSharedFolderVMDir(t)
	sock := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
		conn.Write([]byte(`{"success":true,"status":{"state":"stopped"}}` + "\n"))
	}()
	_, err = ctlVMStatusStateUntil(sock, time.Second, time.Now().Add(20*time.Millisecond))
	listener.Close()
	<-done
	if err == nil {
		t.Fatal("accepted status beyond original deadline")
	}
}
