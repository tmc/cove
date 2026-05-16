package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/knusbaum/go9p"
	ninepfs "github.com/knusbaum/go9p/fs"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

const defaultNinePAddr = "unix:/tmp/cove.9p"

type ninePServeOptions struct {
	Addr string
}

type ninePVMState struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	State    string `json:"state"`
	OSType   string `json:"osType,omitempty"`
	DiskSize int64  `json:"diskSize"`
	Created  string `json:"created,omitempty"`
}

type ninePDiskInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func runNinePCommand(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		printNinePUsage(env.Stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		if len(args) == 2 && isHelpArg(args[1]) {
			printNinePUsage(env.Stdout)
			return 0
		}
		return commandError(env, runNinePServe(context.Background(), env.Stdout, args[1:]))
	case "-h", "-help", "--help", "help":
		printNinePUsage(env.Stdout)
		return 0
	default:
		printNinePUsage(env.Stderr)
		return commandUsageError(env, fmt.Errorf("unknown 9p command %q", args[0]))
	}
}

func printNinePUsage(w io.Writer) {
	fmt.Fprint(w, `usage:
  cove 9p serve [-addr unix:/tmp/cove.9p]

Serve a read-only 9p view of local cove VM metadata.

Options:
  -addr string
        listen address: unix:/path or tcp:127.0.0.1:port (default unix:/tmp/cove.9p)
`)
}

func runNinePServe(ctx context.Context, stdout io.Writer, args []string) error {
	opts := ninePServeOptions{Addr: defaultNinePAddr}
	fs := flag.NewFlagSet("cove 9p serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Addr, "addr", opts.Addr, "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	tree, err := buildNinePVMFS()
	if err != nil {
		return err
	}
	ln, cleanup, err := listenNineP(opts.Addr)
	if err != nil {
		return err
	}
	defer cleanup()
	defer ln.Close()

	fmt.Fprintf(stdout, "serving read-only 9p VM metadata on %s\n", opts.Addr)
	return serveNineP(ctx, ln, tree.Server())
}

func listenNineP(addr string) (net.Listener, func(), error) {
	network, target, err := parseNinePAddr(addr)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {}
	if network == "unix" {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("remove stale 9p socket: %w", err)
		}
		cleanup = func() { _ = os.Remove(target) }
	}
	ln, err := net.Listen(network, target)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("listen 9p: %w", err)
	}
	return ln, cleanup, nil
}

func parseNinePAddr(addr string) (network, target string, err error) {
	kind, value, ok := strings.Cut(addr, ":")
	if !ok || value == "" {
		return "", "", fmt.Errorf("addr must be unix:/path or tcp:127.0.0.1:port")
	}
	switch kind {
	case "unix":
		if !filepath.IsAbs(value) {
			return "", "", fmt.Errorf("unix 9p addr must be absolute")
		}
		return "unix", value, nil
	case "tcp":
		host, _, err := net.SplitHostPort(value)
		if err != nil {
			return "", "", fmt.Errorf("parse tcp 9p addr: %w", err)
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return "", "", fmt.Errorf("tcp 9p addr must bind localhost")
		}
		return "tcp", value, nil
	default:
		return "", "", fmt.Errorf("unsupported 9p addr scheme %q", kind)
	}
}

func serveNineP(ctx context.Context, ln net.Listener, srv go9p.Srv) error {
	errc := make(chan error, 1)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				errc <- err
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				if err := go9p.ServeReadWriter(bufio.NewReader(conn), conn, srv); err != nil && !errors.Is(err, net.ErrClosed) {
					fmt.Fprintf(os.Stderr, "9p: %v\n", err)
				}
			}(conn)
		}
	}()
	err := <-errc
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func buildNinePVMFS() (*ninepfs.FS, error) {
	fsys, root := ninepfs.NewFS("cove", "cove", 0555)
	vmsDir := ninepfs.NewStaticDir(fsys.NewStat("vms", "cove", "cove", 0555))
	if err := root.AddChild(vmsDir); err != nil {
		return nil, err
	}

	infos, err := vmconfig.List(nil)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if err := addNinePVM(fsys, vmsDir, info); err != nil {
			return nil, err
		}
	}
	return fsys, nil
}

func addNinePVM(fsys *ninepfs.FS, parent *ninepfs.StaticDir, info vmconfig.Info) error {
	vmDir := ninepfs.NewStaticDir(fsys.NewStat(info.Name, "cove", "cove", 0555))
	if err := parent.AddChild(vmDir); err != nil {
		return err
	}
	state := ninePVMState{
		Name:     info.Name,
		Path:     info.Path,
		State:    info.State,
		OSType:   info.OSType,
		DiskSize: info.DiskSize,
	}
	if !info.Created.IsZero() {
		state.Created = info.Created.Format("2006-01-02T15:04:05Z07:00")
	}
	if err := addNinePJSON(fsys, vmDir, "state.json", state); err != nil {
		return err
	}
	if err := addNinePFileIfExists(fsys, vmDir, "config.json", filepath.Join(info.Path, "config.json")); err != nil {
		return err
	}
	if err := addNinePFileIfExists(fsys, vmDir, "shared_folders.json", filepath.Join(info.Path, "shared_folders.json")); err != nil {
		return err
	}

	disks, err := listNinePDisks(info.Path)
	if err != nil {
		return err
	}
	return addNinePJSON(fsys, vmDir, "disks.json", disks)
}

func addNinePJSON(fsys *ninepfs.FS, parent *ninepfs.StaticDir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return parent.AddChild(ninepfs.NewStaticFile(fsys.NewStat(name, "cove", "cove", 0444), data))
}

func addNinePFileIfExists(fsys *ninepfs.FS, parent *ninepfs.StaticDir, name, path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return parent.AddChild(ninepfs.NewStaticFile(fsys.NewStat(name, "cove", "cove", 0444), data))
}

func listNinePDisks(dir string) ([]ninePDiskInfo, error) {
	names := []string{"disk.img", "linux-disk.img", "windows-disk.img", "aux.img"}
	var disks []ninePDiskInfo
	for _, name := range names {
		path := filepath.Join(dir, name)
		st, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		if st.IsDir() {
			continue
		}
		disks = append(disks, ninePDiskInfo{Name: name, Path: path, Size: st.Size()})
	}
	return disks, nil
}
