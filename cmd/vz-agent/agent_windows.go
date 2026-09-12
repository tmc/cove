package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func statFilesystem(path string) (uint64, uint64, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return 0, 0, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(name, &available, &total, &free); err != nil {
		return 0, 0, err
	}
	return total, available, nil
}

func resizeTTY(int, uint32, uint32) error {
	return errors.New("tty resize unsupported on windows")
}

func signalExec(pid int, sig int32) error {
	if !allowedExecSignal(sig) {
		return errors.New("unsupported signal")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer process.Release()
	return process.Kill()
}

func allowedExecSignal(sig int32) bool {
	return sig == 9
}

func setSystemTime(time.Time) error {
	return errors.New("set time unsupported on windows")
}

func setUser(*exec.Cmd, string) error {
	return errors.New("set user unsupported on windows")
}

func configureProcessGroup(*exec.Cmd) {}

func shutdownCommand(reboot, force bool) *exec.Cmd {
	action := "/s"
	if reboot {
		action = "/r"
	}
	args := []string{action, "/t", "0"}
	if force {
		args = append(args, "/f")
	}
	return exec.Command("shutdown.exe", args...)
}
