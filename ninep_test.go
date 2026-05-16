package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knusbaum/go9p"
	"github.com/knusbaum/go9p/client"
	ninepfs "github.com/knusbaum/go9p/fs"
	"github.com/knusbaum/go9p/proto"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestParseNinePAddr(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		wantNetwork string
		wantTarget  string
		wantErr     string
	}{
		{"unix", "unix:/tmp/cove.9p", "unix", "/tmp/cove.9p", ""},
		{"tcp", "tcp:127.0.0.1:5640", "tcp", "127.0.0.1:5640", ""},
		{"relative unix", "unix:cove.9p", "", "", "absolute"},
		{"non localhost tcp", "tcp:0.0.0.0:5640", "", "", "localhost"},
		{"missing scheme", "/tmp/cove.9p", "", "", "addr must"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, target, err := parseNinePAddr(tt.addr)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseNinePAddr(%q) error = %v, want %q", tt.addr, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNinePAddr(%q): %v", tt.addr, err)
			}
			if network != tt.wantNetwork || target != tt.wantTarget {
				t.Fatalf("parseNinePAddr(%q) = (%q, %q), want (%q, %q)", tt.addr, network, target, tt.wantNetwork, tt.wantTarget)
			}
		})
	}
}

func TestBuildNinePVMFS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vmDir := filepath.Join(vmconfig.BaseDir(), "ubuntu")
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := vmconfig.Save(vmDir, &vmconfig.Config{CPU: 4, MemoryGB: 8}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "shared_folders.json"), []byte(`{"mounts":[]}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	fsys, err := buildNinePVMFS()
	if err != nil {
		t.Fatalf("buildNinePVMFS: %v", err)
	}
	c := newNinePTestClient(t, fsys.Server())

	stats, err := c.Readdir("/vms/ubuntu")
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}
	gotNames := make(map[string]bool)
	for _, st := range stats {
		gotNames[st.Name] = true
	}
	for _, want := range []string{"config.json", "disks.json", "shared_folders.json", "state.json"} {
		if !gotNames[want] {
			t.Fatalf("/vms/ubuntu missing %s in %#v", want, gotNames)
		}
	}

	data := readNinePTestFile(t, c, "/vms/ubuntu/state.json")
	var state ninePVMState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if state.Name != "ubuntu" || state.OSType != "Linux" || state.DiskSize != 4 {
		t.Fatalf("state = %#v", state)
	}

	data = readNinePTestFile(t, c, "/vms/ubuntu/disks.json")
	var disks []ninePDiskInfo
	if err := json.Unmarshal(data, &disks); err != nil {
		t.Fatalf("unmarshal disks: %v", err)
	}
	if len(disks) != 1 || disks[0].Name != "linux-disk.img" || disks[0].Size != 4 {
		t.Fatalf("disks = %#v", disks)
	}
}

func newNinePTestClient(t *testing.T, srv go9p.Srv) *client.Client {
	t.Helper()
	server, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	go func() {
		defer server.Close()
		_ = go9p.ServeReadWriter(server, server, srv)
	}()
	c, err := client.NewClient(cli, "cove", "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func readNinePTestFile(t *testing.T, c *client.Client, path string) []byte {
	t.Helper()
	f, err := c.Open(path, proto.Oread)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(%q): %v", path, err)
	}
	return data
}

func TestServeNinePStopsWithContext(t *testing.T) {
	fsys, err := buildEmptyNinePVMFS()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serveNineP(ctx, ln, fsys.Server()); err != nil {
		t.Fatalf("serveNineP canceled: %v", err)
	}
}

func buildEmptyNinePVMFS() (*ninepfs.FS, error) {
	fsys, root := ninepfs.NewFS("cove", "cove", 0555)
	return fsys, root.AddChild(ninepfs.NewStaticDir(fsys.NewStat("vms", "cove", "cove", 0555)))
}
