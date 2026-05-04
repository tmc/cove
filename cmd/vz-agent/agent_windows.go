package main

import (
	"errors"
	"os/exec"
	"time"
)

func statFilesystem(string) (uint64, uint64, error) {
	return 0, 0, errors.New("filesystem stats unsupported on windows")
}

func resizeTTY(int, uint32, uint32) error {
	return errors.New("tty resize unsupported on windows")
}

func signalExec(int, int32) error {
	return errors.New("signal exec unsupported on windows")
}

func allowedExecSignal(sig int32) bool {
	return sig == 2 || sig == 15
}

func setSystemTime(time.Time) error {
	return errors.New("set time unsupported on windows")
}

func setUser(*exec.Cmd, string) error {
	return errors.New("set user unsupported on windows")
}

func configureProcessGroup(*exec.Cmd) {}
