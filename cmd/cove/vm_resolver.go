package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tmc/cove/internal/vmconfig"
)

// VMResolveOptions controls how a target VM is resolved.
type VMResolveOptions struct {
	// Command is the name of the command for error messages.
	Command string
	// ExplicitVM is a VM name provided explicitly via flag or option.
	ExplicitVM string
	// PositionalArgs are positional arguments that may contain a VM name.
	PositionalArgs []string
	// PositionalIsOptionalVM indicates that PositionalArgs[0] is only treated
	// as a VM name if it matches an existing valid VM.
	PositionalIsOptionalVM bool
	// RequireRunning indicates that the resolved VM must be currently running.
	RequireRunning bool
	// Stderr specifies where warnings should be written. If nil, os.Stderr is used.
	Stderr io.Writer
}

func (opts VMResolveOptions) stderr() io.Writer {
	if opts.Stderr != nil {
		return opts.Stderr
	}
	return os.Stderr
}

// extractVMFlag searches args for -vm or --vm flags (-vm name, -vm=name,
// --vm name, --vm=name), returns the last specified VM name, and the
// remaining args with the -vm flag and its argument stripped out.
// Flag scanning stops if "--" is encountered.
func extractVMFlag(args []string) (string, []string) {
	var vm string
	var remaining []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			remaining = append(remaining, args[i:]...)
			break
		}
		if arg == "-vm" || arg == "--vm" {
			if i+1 < len(args) {
				i++
				vm = strings.TrimSpace(args[i])
			}
			continue
		}
		if strings.HasPrefix(arg, "-vm=") || strings.HasPrefix(arg, "--vm=") {
			eq := strings.IndexByte(arg, '=')
			vm = strings.TrimSpace(arg[eq+1:])
			continue
		}
		remaining = append(remaining, arg)
	}
	return vm, remaining
}

// isKnownVM reports whether name refers to an existing, valid VM.
func isKnownVM(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	dir, ok := vmconfig.ExistingPath(name)
	if !ok {
		return false
	}
	return vmconfig.Validate(dir)
}

// configuredActiveVM returns the name and directory of the active VM if
// ~/.vz/current exists and points to an existing, valid VM.
func configuredActiveVM() (name, dir string, ok bool) {
	linkPath := vmconfig.CurrentLink()
	target, err := os.Readlink(linkPath)
	if err != nil {
		return "", "", false
	}
	name = vmconfig.NameForPath(target)
	if name == "" {
		return "", "", false
	}
	dir, exists := vmconfig.ExistingPath(name)
	if !exists || !vmconfig.Validate(dir) {
		return "", "", false
	}
	return name, dir, true
}

// singleRunningVM returns the name and directory if exactly one VM is currently running.
func singleRunningVM() (name, dir string, ok bool) {
	vms, err := vmconfig.List(detectVMState)
	if err != nil {
		return "", "", false
	}
	var running []vmconfig.Info
	for _, v := range vms {
		if v.State == "running" {
			running = append(running, v)
		}
	}
	if len(running) == 1 {
		return running[0].Name, running[0].Path, true
	}
	return "", "", false
}

func vmNotFoundError(command, name string) error {
	if command != "" {
		return fmt.Errorf("%s: no VM named %q under %s\n  list VMs: cove list\n  create a VM: cove up -user <name>", command, name, vmconfig.BaseDir())
	}
	return fmt.Errorf("no VM named %q under %s\n  list VMs: cove list\n  create a VM: cove up -user <name>", name, vmconfig.BaseDir())
}

func noVMResolvedError(command string) error {
	if command != "" {
		return fmt.Errorf("%s: no VM specified and no active or running VM found\n  list VMs: cove list\n  create a VM: cove up -user <name>", command)
	}
	return fmt.Errorf("no VM specified and no active or running VM found\n  list VMs: cove list\n  create a VM: cove up -user <name>")
}

func vmNotRunningError(command, name, dir string) error {
	state := detectVMState(dir)
	if command != "" {
		return fmt.Errorf("vm %q is %s; %s requires a running VM\n  start it with: cove run %s\n  list VMs with: cove list", name, state, command, name)
	}
	return fmt.Errorf("vm %q is %s; requires a running VM\n  start it with: cove run %s\n  list VMs with: cove list", name, state, name)
}

func staleActiveVMError(command, target string) error {
	msg := fmt.Sprintf("active VM pointer is stale (%s points to missing %s)\n  set active VM with: cove vm set <name>\n  list VMs: cove list\n  create a VM: cove up -user <name>", vmconfig.CurrentLink(), target)
	if command != "" {
		return fmt.Errorf("%s: %s", command, msg)
	}
	return errors.New(msg)
}

// resolveTargetVM resolves the target VM using the standard precedence:
//  1. Explicit -vm flag
//  2. Positional VM name if applicable/valid
//  3. Fallback to active VM (if configured and valid)
//  4. Fallback to the only running VM when no active VM is configured
//  5. Informative error if no VM could be resolved or the specified VM does not exist.
func resolveTargetVM(opts VMResolveOptions) (name, dir string, err error) {
	// 1. Explicit -vm flag
	if explicit := strings.TrimSpace(opts.ExplicitVM); explicit != "" {
		d, ok := vmconfig.ExistingPath(explicit)
		if !ok || !vmconfig.Validate(d) {
			return "", "", vmNotFoundError(opts.Command, explicit)
		}
		if opts.RequireRunning && detectVMState(d) != "running" {
			return "", "", vmNotRunningError(opts.Command, explicit, d)
		}
		return explicit, d, nil
	}

	// 2. Positional VM name
	if len(opts.PositionalArgs) > 0 {
		pos := strings.TrimSpace(opts.PositionalArgs[0])
		if opts.PositionalIsOptionalVM {
			if isKnownVM(pos) {
				d, _ := vmconfig.ExistingPath(pos)
				if opts.RequireRunning && detectVMState(d) != "running" {
					return "", "", vmNotRunningError(opts.Command, pos, d)
				}
				return pos, d, nil
			}
			// Not a known VM; fall through to fallback resolution.
		} else if pos != "" {
			d, ok := vmconfig.ExistingPath(pos)
			if !ok || !vmconfig.Validate(d) {
				return "", "", vmNotFoundError(opts.Command, pos)
			}
			if opts.RequireRunning && detectVMState(d) != "running" {
				return "", "", vmNotRunningError(opts.Command, pos, d)
			}
			return pos, d, nil
		}
	}

	// 3. Fallback to active VM (if configured and valid)
	activeName, activeDir, activeOK := configuredActiveVM()
	if activeOK {
		if opts.RequireRunning {
			if detectVMState(activeDir) == "running" {
				return activeName, activeDir, nil
			}
			return "", "", vmNotRunningError(opts.Command, activeName, activeDir)
		}
		return activeName, activeDir, nil
	}

	// 4. Stale active VM fallback or error
	if _, staleTarget, isStale := vmconfig.StaleActiveLink(); isStale {
		return "", "", staleActiveVMError(opts.Command, staleTarget)
	}

	// 5. Fallback to the only running VM
	if rName, rDir, rOK := singleRunningVM(); rOK {
		fmt.Fprintf(opts.stderr(), "using only running VM %q; select explicitly with -vm %s\n", rName, rName)
		return rName, rDir, nil
	}

	// 6. Informative error
	return "", "", noVMResolvedError(opts.Command)
}
