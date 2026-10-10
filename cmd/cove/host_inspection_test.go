package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessInfoDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\nexec /bin/sleep 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	start := time.Now()
	if _, ok := processInfo(os.Getpid()); ok {
		t.Fatal("hung ps returned process info")
	}
	if elapsed := time.Since(start); elapsed > 2700*time.Millisecond {
		t.Fatalf("process inspection took %s, want at most 2.7s", elapsed)
	}
}

func TestHostInspectionCancellation(t *testing.T) {
	tests := []struct {
		name, script string
		want         error
	}{
		{"deadline", "exec /bin/sleep 30", context.DeadlineExceeded},
		{"stderr", `echo diagnostic >&2; echo started > "$1"; exec /bin/sleep 30`, context.Canceled},
		{"inherited-pipe", `/bin/sleep 30 & echo $!; echo started > "$1"; exit 0`, exec.ErrWaitDelay},
		{"process-group", `/bin/sleep 30 & echo $!; echo started > "$1"; wait`, context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget := 5 * time.Second
			if tt.name == "deadline" {
				budget = 200 * time.Millisecond
			}
			marker := filepath.Join(t.TempDir(), "started")
			ctx, cancel := context.WithCancel(context.Background())
			type result struct {
				out []byte
				err error
			}
			done := make(chan result, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				out, err := runHostInspection(ctx, budget, "/bin/sh", "-c", tt.script, "inspection-test", marker)
				done <- result{out, err}
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Error("inspection worker did not stop")
				}
			})
			start := time.Now()
			if tt.name != "deadline" {
				limit := time.Now().Add(5 * time.Second)
				for {
					info, err := os.Stat(marker)
					if err == nil {
						start = info.ModTime()
						break
					}
					if !os.IsNotExist(err) {
						t.Fatal(err)
					}
					select {
					case <-finished:
						if _, err := os.Stat(marker); err != nil {
							t.Fatal("inspection finished before startup marker")
						}
						continue
					default:
					}
					if time.Now().After(limit) {
						t.Fatal("inspection did not start")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if tt.name != "inherited-pipe" {
					start = time.Now()
					cancel()
				}
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(time.Second):
				t.Fatal("inspection exceeded deadline and pipe cleanup allowance")
			}
			out, err := got.out, got.err
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v (output %q)", err, tt.want, out)
			}
			if time.Since(start) > time.Second {
				t.Fatal("inspection exceeded deadline and pipe cleanup allowance")
			}
			if tt.name == "deadline" {
				return
			}
			if tt.name == "stderr" {
				if !strings.Contains(string(out), "diagnostic") {
					t.Fatalf("stderr lost: %q", out)
				}
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("exit error lost: %v", err)
				}
				return
			}
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(out)))
			if parseErr != nil {
				t.Fatalf("child pid: %q: %v", out, parseErr)
			}
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			limit := time.Now().Add(time.Second)
			for syscall.Kill(pid, 0) == nil {
				if time.Now().After(limit) {
					t.Fatalf("inspection left child %d alive", pid)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestHostInspectionCanceledCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runHostInspection(ctx, time.Second, "/bin/sleep", "30")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
	if _, ok := processInfoContext(ctx, os.Getpid()); ok {
		t.Fatal("canceled ownership inspection succeeded")
	}
	if !processStartedAtContext(ctx, os.Getpid()).IsZero() {
		t.Fatal("canceled start time inspection succeeded")
	}
	if _, err := readBootPDDefaultsContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("defaults error = %v, want canceled", err)
	}
}

func TestProcessInfoMalformedOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\necho malformed output\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if _, ok := processInfo(os.Getpid()); ok {
		t.Fatal("malformed process info accepted")
	}
	if !processStartedAt(os.Getpid()).IsZero() {
		t.Fatal("malformed start time accepted")
	}
}

func TestHostInspectionFailureOutput(t *testing.T) {
	out, err := runHostInspection(context.Background(), time.Second, "/bin/sh", "-c", "echo failure >&2; exit 7")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("error = %v, want exit status 7", err)
	}
	if strings.TrimSpace(string(out)) != "failure" {
		t.Fatalf("stderr = %q", out)
	}
}

func TestProcessInfoCurrentProcess(t *testing.T) {
	info, ok := processInfo(os.Getpid())
	if !ok || info.PPID != os.Getppid() || info.StartedAt.IsZero() || info.Command == "" {
		t.Fatalf("current process = %+v, %v", info, ok)
	}
}

func TestDoctorHostInspectionErrors(t *testing.T) {
	old := hostDoctorRunCommand
	t.Cleanup(func() { hostDoctorRunCommand = old })
	hostDoctorRunCommand = func(string, ...string) ([]byte, error) { return []byte("probe diagnostic"), context.DeadlineExceeded }
	for _, check := range []hostDoctorCheck{hostDoctorCodesignCheck(), hostDoctorXcodeCheck()} {
		if !strings.Contains(check.Message, "probe diagnostic") || !strings.Contains(check.Message, "context deadline exceeded") {
			t.Fatalf("inspection error omitted: %+v", check)
		}
	}
}
