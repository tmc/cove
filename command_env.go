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
	return currentRuntimeOptions().commandEnv()
}

func (opts runtimeOptions) commandEnv() commandEnv {
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
			Name: opts.VMName,
			Dir:  opts.VMDir,
		},
		Options: commandOptions{
			Verbose:  opts.Verbose,
			Fleet:    opts.Fleet,
			Headless: opts.Headless,
			GUI:      opts.GUI,
			Linux:    opts.Linux,
		},
	}
}
