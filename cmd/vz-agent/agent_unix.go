//go:build darwin || linux

package main

import (
	"fmt"
	"os/exec"
	"os/user"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func statFilesystem(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}

func resizeTTY(fd int, rows, cols uint32) error {
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
}

func signalExec(pid int, sig int32) error {
	return syscall.Kill(-pid, syscall.Signal(sig))
}

func allowedExecSignal(sig int32) bool {
	switch sig {
	case int32(syscall.SIGINT), int32(syscall.SIGTERM), int32(syscall.SIGKILL):
		return true
	default:
		return false
	}
}

func setSystemTime(t time.Time) error {
	tv := unix.NsecToTimeval(t.UnixNano())
	return unix.Settimeofday(&tv)
}

func setUser(cmd *exec.Cmd, username string) error {
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("lookup user %q: %w", username, err)
	}
	var uid, gid uint32
	fmt.Sscanf(u.Uid, "%d", &uid)
	fmt.Sscanf(u.Gid, "%d", &gid)
	ensureSysProcAttr(cmd).Credential = &syscall.Credential{
		Uid: uid,
		Gid: gid,
	}
	return nil
}

func configureProcessGroup(cmd *exec.Cmd) {
	ensureSysProcAttr(cmd).Setpgid = true
}

func ensureSysProcAttr(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	return cmd.SysProcAttr
}
