package main

import (
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperPeerUIDAccept verifies the daemon accepts a connection from the
// authorized UID and rejects others. We run handleHelperConn directly
// (without launchd) so we can test the path without root.
func TestHelperPeerUIDAccept(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	myUID := os.Getuid()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleHelperConn(slog.Default(), conn, myUID)
	}()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if err := json.NewEncoder(conn).Encode(helperRequest{Op: "ping"}); err != nil {
		t.Fatal(err)
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("ping rejected: %+v", resp)
	}
}

func TestHelperPeerUIDReject(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// Allow only an impossible UID.
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleHelperConn(slog.Default(), conn, -999)
	}()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if err := json.NewEncoder(conn).Encode(helperRequest{Op: "ping"}); err != nil {
		// Server may have closed first; that's fine.
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("expected error response, got decode error: %v", err)
	}
	if resp.OK {
		t.Fatalf("expected reject, got ok=true")
	}
}

func TestHelperPingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	myUID := os.Getuid()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleHelperConn(slog.Default(), conn, myUID)
	}()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := json.NewEncoder(conn).Encode(helperRequest{Op: "ping"}); err != nil {
		t.Fatal(err)
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("ping failed: %+v", resp)
	}
}

func TestHelperOpenBlockDeviceRejectsUnsafePath(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "cove-helper-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleHelperConn(slog.Default(), conn, os.Getuid())
	}()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	req := helperRequest{
		Op: "open_block_device",
		OpenBlockDevice: &blockDeviceRequest{
			Path:     "/tmp/disk.img",
			ReadOnly: false,
		},
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("open_block_device accepted unsafe path")
	}
	if resp.Error != "block device /tmp/disk.img is not under /dev" {
		t.Fatalf("error = %q", resp.Error)
	}
}

func TestValidateForceDetachRequestRequiresMatchingAttachedDisk(t *testing.T) {
	oldFind := helperFindAttachedDisk
	t.Cleanup(func() { helperFindAttachedDisk = oldFind })
	helperFindAttachedDisk = func(path string) (string, bool, error) {
		if path != "/Users/tmc/.vz/vms/test/disk.img" {
			t.Fatalf("path = %q", path)
		}
		return "/dev/disk23", true, nil
	}

	device, diskPath, err := validateForceDetachRequest(&forceDetachRequest{
		Device:   "/dev/disk23",
		DiskPath: "/Users/tmc/.vz/vms/test/disk.img",
	})
	if err != nil {
		t.Fatalf("validateForceDetachRequest: %v", err)
	}
	if device != "/dev/disk23" || diskPath != "/Users/tmc/.vz/vms/test/disk.img" {
		t.Fatalf("device,diskPath = %q,%q", device, diskPath)
	}
}

func TestValidateForceDetachRequestRejectsDeviceMismatch(t *testing.T) {
	oldFind := helperFindAttachedDisk
	t.Cleanup(func() { helperFindAttachedDisk = oldFind })
	helperFindAttachedDisk = func(string) (string, bool, error) {
		return "/dev/disk24", true, nil
	}

	_, _, err := validateForceDetachRequest(&forceDetachRequest{
		Device:   "/dev/disk23",
		DiskPath: "/Users/tmc/.vz/vms/test/disk.img",
	})
	if err == nil {
		t.Fatal("validateForceDetachRequest returned nil error")
	}
	if want := "attached as /dev/disk24, not /dev/disk23"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestHelperForceDetachFallsBackToDiskutil(t *testing.T) {
	oldCmd := helperDetachCommand
	t.Cleanup(func() { helperDetachCommand = oldCmd })
	var calls []string
	helperDetachCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "hdiutil" {
			return []byte("busy"), os.ErrPermission
		}
		return nil, nil
	}
	if err := helperForceDetachDevice("/dev/disk23"); err != nil {
		t.Fatalf("helperForceDetachDevice: %v", err)
	}
	want := []string{
		"hdiutil detach /dev/disk23 -force",
		"diskutil unmountDisk force /dev/disk23",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestHelperInstallOwnerUID(t *testing.T) {
	tests := []struct {
		name    string
		uid     int
		env     map[string]string
		want    int
		wantErr bool
	}{
		{name: "normal user", uid: 501, want: 501},
		{name: "sudo user", uid: 0, env: map[string]string{"SUDO_UID": "501"}, want: 501},
		{name: "sudo uid trims spaces", uid: 0, env: map[string]string{"SUDO_UID": " 502\n"}, want: 502},
		{name: "root without sudo owner", uid: 0, wantErr: true},
		{name: "bad sudo uid", uid: 0, env: map[string]string{"SUDO_UID": "nope"}, wantErr: true},
		{name: "root sudo uid", uid: 0, env: map[string]string{"SUDO_UID": "0"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := helperInstallOwnerUID(tt.uid, func(key string) (string, bool) {
				v, ok := tt.env[key]
				return v, ok
			})
			if tt.wantErr {
				if err == nil {
					t.Fatal("helperInstallOwnerUID() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("helperInstallOwnerUID(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("helperInstallOwnerUID() = %d, want %d", got, tt.want)
			}
		})
	}
}
