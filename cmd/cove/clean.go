package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

var cleanWaitNotRunningTimeout = 3 * time.Second

func newCleanFlagSet(w io.Writer) (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(w)
	target := fs.String("vm", "", "target VM name")
	var yes bool
	fs.BoolVar(&yes, "y", false, "skip confirmation prompt")
	fs.BoolVar(&yes, "yes", false, "skip confirmation prompt")
	fs.Usage = func() { printCleanUsage(w) }
	return fs, target, &yes
}

func handleCleanCommand(env commandEnv, args []string) int {
	fs, vmFlag, yes := newCleanFlagSet(env.Stderr)
	if err := parseFlagsOrHelp(fs, moveKnownFlagsFirst(args, map[string]bool{
		"y": false, "yes": false, "vm": true,
	})); err != nil {
		if errors.Is(err, errFlagHelp) {
			return 0
		}
		return commandUsageError(env, err)
	}

	effectiveVM := *vmFlag
	if effectiveVM == "" {
		effectiveVM = vmName
	}
	target := currentVMSelection()
	if effectiveVM != "" {
		if effectiveVM == vmName && vmDir != "" {
			target = vmSelection{Directory: vmDir, Name: vmName}
		} else {
			target = vmSelection{
				Directory: vmconfig.Path(effectiveVM),
				Name:      effectiveVM,
			}
		}
	}

	if fs.NArg() == 1 {
		name := fs.Arg(0)
		if effectiveVM != "" && effectiveVM != name {
			return commandUsageError(env, fmt.Errorf("conflicting VM names: %s and %s", effectiveVM, name))
		}
		if name == vmName && vmDir != "" {
			target = vmSelection{Directory: vmDir, Name: vmName}
		} else {
			target = vmSelection{
				Directory: vmconfig.Path(name),
				Name:      name,
			}
		}
	} else if fs.NArg() > 1 {
		return commandUsageError(env, fmt.Errorf("too many arguments: %s", strings.Join(fs.Args(), " ")))
	}

	if target.Directory == "" {
		resolvedName, resolvedDir, err := resolveTargetVM(VMResolveOptions{
			Command:    "clean",
			ExplicitVM: effectiveVM,
		})
		if err != nil {
			return commandError(env, err)
		}
		target = vmSelection{Directory: resolvedDir, Name: resolvedName}
	}

	targetName := target.Name
	if targetName == "" {
		targetName = filepath.Base(target.Directory)
	}
	if targetName == "." || targetName == "" {
		targetName = "default"
	}

	if _, err := os.Stat(target.Directory); os.IsNotExist(err) {
		return commandError(env, fmt.Errorf("clean: no VM named %q under %s", targetName, vmconfig.BaseDir()))
	}

	if isVMRunningAt(target.Directory) && !waitForVMNotRunning(target.Directory, cleanWaitNotRunningTimeout) {
		return commandError(env, cleanRunningVMError(targetName))
	}

	if !*yes {
		prompt := fmt.Sprintf("Clean VM %q? This cannot be undone. [y/N] ", targetName)
		ok, err := confirmDeletef("%s", prompt)
		if err != nil {
			if strings.Contains(err.Error(), "requires confirmation") {
				return 2
			}
			return 1
		}
		if !ok {
			return 1
		}
	}

	return commandError(env, cleanVMForVM(target))
}

func cleanRunningVMError(name string) error {
	retry := "cove clean"
	if name != "" && name != vmconfig.ActiveName() {
		retry = fmt.Sprintf("cove clean -vm %s", name)
	}
	return fmt.Errorf("cannot clean VM %q: it is currently running\n  request stop: cove ctl -vm %s request-stop\n  check status: cove list\n  if still running: cove ctl -vm %s stop\n  then retry: %s", name, name, name, retry)
}
