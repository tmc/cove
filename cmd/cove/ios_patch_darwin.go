package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tmc/cove/internal/ios/firmware"
	"github.com/tmc/cove/internal/vmconfig"
)

func runIOSFirmwarePatch(env commandEnv, args []string) int {
	flags := flag.NewFlagSet("ios firmware patch", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	var options firmware.PatchOptions
	flags.StringVar(&options.Toolchain, "toolchain", filepath.Join(vmconfig.StateDir(), "ios", "toolchains", firmware.SourceCommit), "pinned toolchain with built patcher")
	flags.StringVar(&options.Prepared, "prepared", "", "directory containing prepared firmware.json")
	flags.StringVar(&options.ROM, "rom", "", "AVPBooter image")
	flags.StringVar(&options.Variant, "variant", "regular", "less, regular, dev, jb or exp")
	flags.BoolVar(&options.NoBinpack, "no-binpack", false, "omit binpack (less only)")
	flags.BoolVar(&options.NoVphoned, "no-vphoned", false, "omit vphoned (less only)")
	flags.BoolVar(&options.ForceExcGuard, "force-exc-guard", false, "force EXC_GUARD patch (regular, jb or exp)")
	flags.BoolVar(&options.Frida, "frida", false, "enable Frida kernel patches (jb or exp)")
	flags.BoolVar(&options.Quiet, "quiet", false, "retain progress in patch.log without printing it")
	flags.StringVar(&options.Python, "python", "", "provisioned Python for less filesystem patching")
	flags.StringVar(&options.SealDirectory, "seal-dir", "", "directory containing versioned apfs_sealvolume tool (less)")
	recordsOut := flags.String("records-out", "", "export validated patch records to this file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return commandUsageError(env, fmt.Errorf("usage: cove ios firmware patch -prepared DIR -rom FILE [flags] OUTPUT"))
	}
	options.Output = flags.Arg(0)
	var err error
	options.Helper, err = os.Executable()
	if err != nil {
		return commandError(env, err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := firmware.Patch(ctx, options, env.Stderr); err != nil {
		return commandError(env, err)
	}
	if *recordsOut != "" {
		data, err := os.ReadFile(filepath.Join(options.Output, "patched.json"))
		if err != nil {
			return commandError(env, err)
		}
		var state struct {
			Attempt string `json:"attempt"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return commandError(env, err)
		}
		if !filepath.IsLocal(state.Attempt) {
			return commandError(env, fmt.Errorf("invalid patch attempt path"))
		}
		data, err = os.ReadFile(filepath.Join(options.Output, state.Attempt, "records.json"))
		if err != nil {
			return commandError(env, err)
		}
		if err := os.WriteFile(*recordsOut, data, 0600); err != nil {
			return commandError(env, err)
		}
	}
	fmt.Fprintln(env.Stdout, filepath.Join(options.Output, "patched.json"))
	return 0
}
