package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
