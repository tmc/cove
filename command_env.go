package main

import (
	"log/slog"
	"os"

	"github.com/tmc/cove/internal/covecli"
)

type commandEnv = covecli.Env
type commandVMSelection = covecli.VMSelection
type commandOptions = covecli.Options

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
