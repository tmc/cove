package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tmc/cove/internal/checkpoint"
)

func runCheckpointCommand(env commandEnv, _ string, args []string) int {
	env = env.WithDefaultIO()
	if len(args) == 0 || isHelpArg(args[0]) {
		printCheckpointUsage(env.Stdout)
		return 0
	}
	return commandError(env, handleCheckpointCommand(env, args))
}

func printCheckpointUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove checkpoint <plan|save|inspect|restore|recover> [flags] <vm> [name]

Flags:
  -disk <path>  Declare another bundle-relative disk (repeatable)
  -json         Print JSON

Cold checkpoints capture declared disk, firmware, identity and config files
of a stopped VM. Shared-folder contents and guest memory are excluded.
Save and restore require a name. Recover finishes an interrupted restore.
Live and paired-memory checkpoints are not yet supported.`)
}

type checkpointDisks []string

func (d *checkpointDisks) String() string     { return fmt.Sprint([]string(*d)) }
func (d *checkpointDisks) Set(s string) error { *d = append(*d, s); return nil }

func handleCheckpointCommand(env commandEnv, args []string) error {
	verb := args[0]
	switch verb {
	case "plan", "save", "inspect", "restore", "recover":
	default:
		return fmt.Errorf("unknown checkpoint command %q", verb)
	}
	fs := flag.NewFlagSet("checkpoint "+verb, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var disks checkpointDisks
	fs.Var(&disks, "disk", "bundle-relative disk")
	jsonOutput := fs.Bool("json", false, "print JSON")
	memory := fs.Bool("memory", false, "paired memory (not yet supported)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCheckpointUsage(env.Stdout)
			return nil
		}
		return err
	}
	if *memory {
		return fmt.Errorf("paired-memory checkpoints require qualified live capture and are not yet supported")
	}
	want := 1
	if verb == "save" || verb == "restore" || verb == "inspect" {
		want = 2
	}
	if fs.NArg() != want {
		return fmt.Errorf("usage: cove checkpoint %s [flags] <vm> [name]", verb)
	}
	if verb != "plan" && verb != "save" && len(disks) != 0 {
		return fmt.Errorf("-disk is only accepted by checkpoint plan or save")
	}
	root, err := requireExistingVMDir("checkpoint", fs.Arg(0))
	if err != nil {
		return err
	}
	manager := checkpoint.New(root)
	var result any
	switch verb {
	case "plan":
		result, err = coldCheckpointInventory(root, disks)
	case "inspect":
		result, err = manager.Read(fs.Arg(1))
	default:
		if err := denyAppleAppSandboxHostAccess("checkpoint " + verb); err != nil {
			return err
		}
		lock, err := acquireRunLock(root, verb == "recover")
		if err != nil {
			return err
		}
		defer lock.Release()
		if controlSocketResponds(root) {
			return fmt.Errorf("stop VM before checkpoint %s", verb)
		}
		if proc, found, err := liveVMProcessForDirectory(root, defaultVMProcessCollector()); err != nil {
			return fmt.Errorf("verify checkpoint VM is stopped: %w", err)
		} else if found {
			return fmt.Errorf("VM process %d is active; stop it before checkpoint %s", proc.PID, verb)
		}
		var paths []string
		var inventory checkpointInventory
		if verb == "recover" {
			pending, e := manager.Pending()
			if e != nil {
				return e
			}
			if !pending {
				return fmt.Errorf("no checkpoint restore recovery is pending")
			}
			paths, err = manager.RecoveryPaths()
		} else {
			inventory, err = coldCheckpointInventory(root, disks)
			if err == nil {
				for _, file := range inventory.Files {
					paths = append(paths, file.Path)
				}
				if verb == "restore" {
					var manifest checkpoint.Manifest
					manifest, err = manager.Read(fs.Arg(1))
					if err == nil {
						err = validateColdCheckpointManifest(inventory, manifest)
					}
					for _, file := range manifest.Files {
						paths = append(paths, file.Path)
					}
				}
			}
		}
		if err != nil {
			return err
		}
		if err := checkpointFilesClosed(root, paths, openFileHolderPIDs); err != nil {
			return err
		}
		switch verb {
		case "save":
			result, err = manager.Save(fs.Arg(1), inventory.Compatibility, inventory.Files)
		case "restore":
			err = manager.Restore(fs.Arg(1), inventory.Compatibility)
			result = map[string]string{"status": "restored", "name": fs.Arg(1)}
		case "recover":
			err = manager.Recover()
			result = map[string]string{"status": "recovered"}
		}
	}
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(env.Stdout).Encode(result)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(env.Stdout, string(data))
	return err
}

func checkpointFilesClosed(root string, paths []string, holders func(string) ([]int, error)) error {
	seen := make(map[string]bool)
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		pids, err := holders(filepath.Join(root, path))
		if err != nil {
			return fmt.Errorf("verify checkpoint file %s is closed: %w", path, err)
		}
		if len(pids) != 0 {
			return fmt.Errorf("checkpoint file %s is open by PID %d; stop its owner first", path, pids[0])
		}
	}
	return nil
}
