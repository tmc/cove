package main

import (
	"io"
	"log/slog"
	"os"
)

type commandEnv struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Logger *slog.Logger

	VM      commandVMSelection
	Options commandOptions
}

type commandVMSelection struct {
	Name string
	Dir  string
}

type commandOptions struct {
	Verbose  bool
	Fleet    string
	Headless bool
	GUI      bool
	Linux    bool
}

func newCommandEnv() commandEnv {
	logger := slog.Default()
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return commandEnv{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Logger: logger,
		VM: commandVMSelection{
			Name: vmName,
			Dir:  vmDir,
		},
		Options: commandOptions{
			Verbose:  verbose,
			Fleet:    fleetName,
			Headless: headlessMode,
			GUI:      guiMode,
			Linux:    linuxMode,
		},
	}
}

func (env commandEnv) withDefaultIO() commandEnv {
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	return env
}
