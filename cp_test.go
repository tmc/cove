package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestCpParseSpec(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		src  string
		dst  string
		want cpSpec
		err  string
	}{
		{
			name: "host to guest",
			src:  "file.txt",
			dst:  "vm1:/tmp/file.txt",
			want: cpSpec{Direction: cpHostToGuest, VM: "vm1", HostPath: filepath.Join(wd, "file.txt"), GuestPath: "/tmp/file.txt"},
		},
		{
			name: "guest to host",
			src:  "vm1:/tmp/file.txt",
			dst:  "out.txt",
			want: cpSpec{Direction: cpGuestToHost, VM: "vm1", GuestPath: "/tmp/file.txt", HostPath: filepath.Join(wd, "out.txt")},
		},
		{
			name: "absolute host",
			src:  "/tmp/in.txt",
			dst:  "vm2:/var/tmp/in.txt",
			want: cpSpec{Direction: cpHostToGuest, VM: "vm2", HostPath: "/tmp/in.txt", GuestPath: "/var/tmp/in.txt"},
		},
		{name: "two local", src: "a", dst: "b", err: "exactly one path must be remote"},
		{name: "two remote", src: "vm:/a", dst: "vm:/b", err: "exactly one path must be remote"},
		{name: "relative guest", src: "a", dst: "vm:tmp/a", err: "guest path must be absolute"},
		{name: "empty vm", src: "a", dst: ":/tmp/a", err: "invalid remote path"},
		{name: "colon host", src: "a:b", dst: "out", err: "guest path must be absolute"},
		{name: "empty src", src: "", dst: "vm:/tmp/a", err: "empty path"},
		{name: "empty dst", src: "a", dst: "", err: "empty path"},
		{name: "vm with slash", src: "a", dst: "vm/sub:/tmp/a", err: "invalid VM name"},
		{name: "remote empty path", src: "a", dst: "vm:", err: "invalid remote path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCpSpec(tc.src, tc.dst)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("parseCpSpec error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCpSpec: %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseCpSpec = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCpParseSpecForVMFlag(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		src    string
		dst    string
		vmFlag string
		want   cpSpec
		err    string
	}{
		{
			name:   "matching host to guest",
			src:    "file.txt",
			dst:    "vm1:/tmp/file.txt",
			vmFlag: "vm1",
			want:   cpSpec{Direction: cpHostToGuest, VM: "vm1", HostPath: filepath.Join(wd, "file.txt"), GuestPath: "/tmp/file.txt"},
		},
		{
			name:   "matching guest to host",
			src:    "vm1:/tmp/file.txt",
			dst:    "out.txt",
			vmFlag: "vm1",
			want:   cpSpec{Direction: cpGuestToHost, VM: "vm1", GuestPath: "/tmp/file.txt", HostPath: filepath.Join(wd, "out.txt")},
		},
		{name: "mismatch", src: "file.txt", dst: "vm1:/tmp/file.txt", vmFlag: "vm2", err: `-vm "vm2" does not match remote endpoint VM "vm1"`},
		{name: "bad flag vm", src: "file.txt", dst: "vm1:/tmp/file.txt", vmFlag: "bad/name", err: "invalid VM name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCpSpecForVM(tt.src, tt.dst, tt.vmFlag)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("parseCpSpecForVM error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCpSpecForVM: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseCpSpecForVM = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCpRoundTripWithFakeAgent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("hello from host\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fake := newFakeCpAgent()
	newAgent := func(vm string) cpAgent {
		if vm != "vm1" {
			t.Fatalf("vm = %q, want vm1", vm)
		}
		return fake
	}
	if err := runCp(context.Background(), []string{src, "vm1:/tmp/data.txt"}, newAgent); err != nil {
		t.Fatalf("host to guest: %v", err)
	}
	if got := string(fake.guest["/tmp/data.txt"]); got != "hello from host\n" {
		t.Fatalf("guest data = %q", got)
	}
	if err := runCp(context.Background(), []string{"vm1:/tmp/data.txt", dst}, newAgent); err != nil {
		t.Fatalf("guest to host: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello from host\n" {
		t.Fatalf("round trip = %q", data)
	}
}

type fakeCpAgent struct {
	guest map[string][]byte
}

func newFakeCpAgent() *fakeCpAgent {
	return &fakeCpAgent{guest: make(map[string][]byte)}
}

func (f *fakeCpAgent) CopyToGuest(_ context.Context, hostPath, guestPath string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return err
	}
	f.guest[guestPath] = append([]byte(nil), data...)
	return nil
}

func (f *fakeCpAgent) CopyFromGuest(_ context.Context, guestPath, hostPath string) error {
	data := f.guest[guestPath]
	if err := os.MkdirAll(filepath.Dir(hostPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(hostPath, data, 0644)
}

func TestCpRunCpArgErrors(t *testing.T) {
	noAgent := func(string) cpAgent { t.Fatal("agent should not be constructed"); return nil }
	for _, tc := range []struct {
		name string
		args []string
		err  string
	}{
		{name: "no args", args: nil, err: "usage:"},
		{name: "one arg", args: []string{"a"}, err: "usage:"},
		{name: "three args", args: []string{"a", "b", "c"}, err: "usage:"},
		{name: "bad spec", args: []string{"a", "b"}, err: "exactly one path must be remote"},
		{name: "vm mismatch", args: []string{"-vm", "other", "a", "vm:/tmp/a"}, err: "does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runCp(context.Background(), tc.args, noAgent)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("runCp err = %v, want %q", err, tc.err)
			}
		})
	}
}

func TestCpAgentErrorPropagation(t *testing.T) {
	wantErr := errors.New("agent unreachable")
	newAgent := func(string) cpAgent { return errCpAgent{err: wantErr} }
	if err := runCp(context.Background(), []string{"a", "vm:/tmp/a"}, newAgent); !errors.Is(err, wantErr) {
		t.Fatalf("host->guest err = %v, want %v", err, wantErr)
	}
	if err := runCp(context.Background(), []string{"vm:/tmp/a", "out"}, newAgent); !errors.Is(err, wantErr) {
		t.Fatalf("guest->host err = %v, want %v", err, wantErr)
	}
}

func TestRunCpCommandUsageErrorsExitTwo(t *testing.T) {
	var stderr bytes.Buffer
	env := commandEnv{Stderr: &stderr}
	if got := runCpCommand(env, "cp", []string{"-vm", "vm1"}); got != 2 {
		t.Fatalf("runCpCommand exit = %d, want 2", got)
	}
	out := stderr.String()
	for _, want := range []string{"Usage: cove cp", "error: usage: cove cp"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stderr = %q, want %q", out, want)
		}
	}
}

func TestCpVMNotFoundBeforeControlSocketHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := runCp(context.Background(), []string{"host.txt", "deleted-vm:/tmp/host.txt"}, newControlCpAgent)
	if err == nil {
		t.Fatal("runCp succeeded for missing VM")
	}
	msg := err.Error()
	for _, want := range []string{`no VM named "deleted-vm"`, vmconfig.BaseDir()} {
		if !strings.Contains(msg, want) {
			t.Fatalf("runCp error = %q, want %q", msg, want)
		}
	}
	for _, notWant := range []string{"control socket not found", "start it with"} {
		if strings.Contains(msg, notWant) {
			t.Fatalf("runCp error = %q, did not want %q", msg, notWant)
		}
	}
}

func TestCpGlobalMissingVMDoesNotCreateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "deleted-global-cp-vm"
	vmDir = ""
	src := filepath.Join(home, "host.txt")
	if err := os.WriteFile(src, []byte("host"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	err := runCp(context.Background(), []string{src, vmName + ":/tmp/host.txt"}, newControlCpAgent)
	if err == nil {
		t.Fatal("runCp succeeded for missing global VM")
	}
	msg := err.Error()
	for _, want := range []string{`no VM named "deleted-global-cp-vm"`, vmconfig.BaseDir()} {
		if !strings.Contains(msg, want) {
			t.Fatalf("runCp error = %q, want %q", msg, want)
		}
	}
	for _, notWant := range []string{"control socket not found", "start it with"} {
		if strings.Contains(msg, notWant) {
			t.Fatalf("runCp error = %q, did not want %q", msg, notWant)
		}
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), vmName)); !os.IsNotExist(err) {
		t.Fatalf("global cp VM dir stat = %v, want not exist", err)
	}
}

func TestCpStoppedExistingVMKeepsControlSocketHint(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cove-cp-test-")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	vm := "stopped-vm"
	dir := filepath.Join(vmconfig.BaseDir(), vm)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir VM: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	err = runCp(context.Background(), []string{"host.txt", vm + ":/tmp/host.txt"}, newControlCpAgent)
	if err == nil {
		t.Fatal("runCp succeeded for stopped VM without control socket")
	}
	msg := err.Error()
	for _, want := range []string{"vm is not running: control socket not found", "start it with"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("runCp error = %q, want %q", msg, want)
		}
	}
	if strings.Contains(msg, "no VM named") {
		t.Fatalf("runCp error = %q, did not want not-found diagnostic", msg)
	}
}

type errCpAgent struct{ err error }

func (e errCpAgent) CopyToGuest(context.Context, string, string) error   { return e.err }
func (e errCpAgent) CopyFromGuest(context.Context, string, string) error { return e.err }
