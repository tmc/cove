package fleet

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Tunnel struct {
	io.ReadWriteCloser
	LocalSocketPath string
	cmd             *exec.Cmd
	localDir        string
}

func DialControlSocket(ctx context.Context, remote Remote, vm string) (*Tunnel, error) {
	vm = defaultVM(remote, vm)
	if vm == "" {
		return nil, fmt.Errorf("fleet vm required")
	}
	dir, err := os.MkdirTemp("", "cove-fleet-*")
	if err != nil {
		return nil, fmt.Errorf("create tunnel dir: %w", err)
	}
	localSock := filepath.Join(dir, "control.sock")
	remoteSock := RemoteControlSocketPath(remote, vm)
	cmd := exec.CommandContext(ctx, sshBinary(), SSHForwardArgs(remote, localSock, remoteSock)...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start ssh tunnel: %w", err)
	}
	conn, err := waitUnixSocket(ctx, localSock)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &Tunnel{ReadWriteCloser: conn, LocalSocketPath: localSock, cmd: cmd, localDir: dir}, nil
}

func (t *Tunnel) LocalSocket() string {
	if t == nil {
		return ""
	}
	return t.LocalSocketPath
}

func (t *Tunnel) Close() error {
	var err error
	if t.ReadWriteCloser != nil {
		err = t.ReadWriteCloser.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}
	if t.localDir != "" {
		_ = os.RemoveAll(t.localDir)
	}
	return err
}

func SSHForwardArgs(remote Remote, localSock, remoteSock string) []string {
	args := []string{"-N", "-L", localSock + ":" + remoteSock}
	args = append(args, remote.SSHArgs...)
	args = append(args, remoteTarget(remote))
	return args
}

func RemoteControlSocketPath(remote Remote, vm string) string {
	return filepath.Join(".vz", "vms", vm, "control.sock")
}

func RemoteControlTokenPath(remote Remote, vm string) string {
	return filepath.Join(".vz", "vms", vm, "control.token")
}

func ReadControlToken(ctx context.Context, remote Remote, vm string) (string, error) {
	vm = defaultVM(remote, vm)
	if vm == "" {
		return "", fmt.Errorf("fleet vm required")
	}
	args := append([]string{}, remote.SSHArgs...)
	args = append(args, remoteTarget(remote), "cat", RemoteControlTokenPath(remote, vm))
	data, err := exec.CommandContext(ctx, sshBinary(), args...).Output()
	if err != nil {
		return "", fmt.Errorf("read remote control token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("remote control token is empty")
	}
	return token, nil
}

func sshBinary() string {
	if path := os.Getenv("COVE_FLEET_SSH"); path != "" {
		return path
	}
	return "ssh"
}

func remoteTarget(remote Remote) string {
	if remote.User != "" {
		return remote.User + "@" + remote.Host
	}
	return remote.Host
}

func defaultVM(remote Remote, vm string) string {
	if vm != "" {
		return vm
	}
	return remote.DefaultVM
}

func waitUnixSocket(ctx context.Context, path string) (net.Conn, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait fleet tunnel %s: %w", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
