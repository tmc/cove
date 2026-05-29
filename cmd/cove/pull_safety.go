package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

const pullTargetProbeTimeout = 500 * time.Millisecond

func ensurePullTargetInactive(vmDirectory string) error {
	return ensurePullTargetInactiveWithTimeout(vmDirectory, pullTargetProbeTimeout)
}

func ensurePullTargetInactiveWithTimeout(vmDirectory string, timeout time.Duration) error {
	sock := GetControlSocketPathForVM(vmDirectory)
	active, err := probeControlSocket(sock, timeout)
	if err != nil {
		return err
	}
	if active {
		name := filepath.Base(vmDirectory)
		return fmt.Errorf("cove pull: cannot pull into an active VM %q. Stop the VM first: cove ctl stop %s", name, name)
	}
	return nil
}

func probeControlSocket(sock string, timeout time.Duration) (bool, error) {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		_ = os.Remove(sock)
		return false, nil
	}
	defer conn.Close()

	if timeout <= 0 {
		timeout = pullTargetProbeTimeout
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return false, fmt.Errorf("probe control socket %s: %w", sock, err)
	}
	req := &controlpb.ControlRequest{Type: "ping"}
	data, err := protojsonMarshaler.Marshal(req)
	if err != nil {
		return false, fmt.Errorf("probe control socket %s: marshal: %w", sock, err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return false, fmt.Errorf("probe control socket %s: write: %w", sock, err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return false, fmt.Errorf("probe control socket %s: read: %w", sock, err)
	}
	var resp controlpb.ControlResponse
	if err := protojsonUnmarshaler.Unmarshal(line, &resp); err != nil {
		return false, fmt.Errorf("probe control socket %s: parse: %w", sock, err)
	}
	return true, nil
}

func checkIncompletePullDisk(vmDirectory, diskPath string) error {
	partial := pullPartialDiskPath(diskPath)
	if _, err := os.Stat(partial); err == nil {
		name := filepath.Base(vmDirectory)
		return fmt.Errorf("cove: VM %s has incomplete disk (pull was interrupted). Delete %s and rerun cove pull, or use cove pull --resume <ref> to continue", name, partial)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat partial disk: %w", err)
	}
	return nil
}

func pullPartialDiskPath(diskPath string) string {
	return diskPath + ".partial"
}
