package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/cove/internal/ios"
	"github.com/tmc/cove/internal/ios/bundle"
	"github.com/tmc/cove/internal/ios/firmware"
	"github.com/tmc/cove/internal/vmconfig"
)

func runIOSCommand(env commandEnv, _ string, args []string) int {
	if len(args) > 0 && args[0] == "firmware" {
		return runIOSFirmware(env, args[1:])
	}
	if len(args) > 0 && args[0] == "devices" {
		return runIOSDevices(env, args[1:])
	}
	if len(args) > 0 && args[0] == "setup" {
		return runIOSSetup(env, args[1:])
	}
	if len(args) > 0 && args[0] == "config" {
		return runIOSConfig(env, args[1:])
	}
	if len(args) > 0 && args[0] == "run" {
		return runIOSRun(env, args[1:])
	}
	if len(args) > 0 && args[0] == "new" {
		return runIOSNew(env, args[1:])
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(env.Stdout, "usage: cove ios preflight | devices [-libusb PATH] | new [-cpu N] [-memory GB] [-disk GB] NAME | setup source|patcher [flags] | firmware prepare [flags] OUTPUT | config [flags] NAME | run [flags] NAME")
		return 0
	}
	if len(args) != 1 || args[0] != "preflight" {
		return commandUsageError(env, fmt.Errorf("usage: cove ios preflight | devices [-libusb PATH] | new [-cpu N] [-memory GB] [-disk GB] NAME | setup source|patcher [flags] | firmware prepare [flags] OUTPUT | config [flags] NAME | run [flags] NAME"))
	}
	executable, err := os.Executable()
	if err != nil {
		return commandError(env, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	report := ios.InspectHost(ctx, executable)
	if err := json.NewEncoder(env.Stdout).Encode(report); err != nil {
		return commandError(env, err)
	}
	if report.Architecture != "arm64" || !report.SignatureValid || !report.DescriptorABI || len(report.MissingEntitlements) != 0 || len(report.InactiveEntitlements) != 0 || len(report.Errors) != 0 {
		return 1
	}
	return 0
}

func runIOSNew(env commandEnv, args []string) int {
	flags := flag.NewFlagSet("ios new", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	cpu := flags.Uint("cpu", 8, "virtual CPU count")
	memory := flags.Uint64("memory", 8, "memory in GiB")
	disk := flags.Int64("disk", 64, "disk size in GiB")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return commandUsageError(env, fmt.Errorf("usage: cove ios new [-cpu N] [-memory GB] [-disk GB] NAME"))
	}
	name := flags.Arg(0)
	if err := validateNewVMName(name); err != nil {
		return commandUsageError(env, err)
	}
	if name != strings.TrimSpace(name) {
		return commandUsageError(env, fmt.Errorf("vm name must not have surrounding whitespace"))
	}
	if *disk <= 0 || *disk > (1<<63-1)/(1<<30) {
		return commandUsageError(env, fmt.Errorf("invalid disk size"))
	}
	if _, ok := vmconfig.ExistingPath(name); ok {
		return commandError(env, fmt.Errorf("vm already exists: %s", name))
	}
	if err := os.MkdirAll(vmconfig.BaseDir(), 0755); err != nil {
		return commandError(env, err)
	}
	dir := filepath.Join(vmconfig.BaseDir(), vmconfig.PackageName(name))
	if err := ios.CreateBundle(dir, bundle.DefaultConfig(), *cpu, *memory, *disk<<30); err != nil {
		return commandError(env, err)
	}
	fmt.Fprintln(env.Stdout, dir)
	return 0
}

func runIOSRun(env commandEnv, args []string) (code int) {
	flags := flag.NewFlagSet("ios run", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	initialize := flags.Bool("initialize", false, "create identity for a new bundle")
	forceDFU := flags.Bool("force-dfu", false, "start in DFU mode")
	stop1 := flags.Bool("stop-in-iboot-stage1", false, "stop in iBoot stage 1")
	stop2 := flags.Bool("stop-in-iboot-stage2", false, "stop in iBoot stage 2")
	debugPort := flags.Uint("debug-port", 0, "AP debug port (0 for automatic)")
	timeout := flags.Duration("timeout", 0, "stop after this duration (0 until interrupted)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return commandUsageError(env, fmt.Errorf("usage: cove ios run [flags] NAME"))
	}
	if *debugPort > 65535 || (*debugPort != 0 && *debugPort < 6000) || *timeout < 0 {
		return commandUsageError(env, fmt.Errorf("invalid ios debug port or timeout"))
	}
	name := flags.Arg(0)
	if err := validateNewVMName(name); err != nil {
		return commandUsageError(env, err)
	}
	dir, ok := vmconfig.ExistingPath(name)
	if !ok {
		return commandError(env, fmt.Errorf("vm not found: %s", name))
	}
	config, err := vmconfig.Load(dir)
	if err != nil {
		return commandError(env, err)
	}
	if config.IOS == nil {
		return commandError(env, fmt.Errorf("vm is not an ios guest: %s", name))
	}
	executable, err := os.Executable()
	if err != nil {
		return commandError(env, err)
	}
	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), 15*time.Second)
	report := ios.InspectHost(preflightCtx, executable)
	preflightCancel()
	if report.Architecture != "arm64" || !report.SignatureValid || !report.DescriptorABI || len(report.MissingEntitlements) != 0 || len(report.InactiveEntitlements) != 0 || len(report.Errors) != 0 {
		return commandError(env, fmt.Errorf("ios host preflight failed; run cove ios preflight with a research-signed build"))
	}
	lock, err := AcquireRunLock(dir)
	if err != nil {
		return commandError(env, err)
	}
	var session *ios.Session
	defer func() {
		if session != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			stopErr := session.Stop(stopCtx)
			cancel()
			if stopErr != nil {
				commandError(env, stopErr)
				code = 1
			}
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			closeErr := session.Close(closeCtx)
			closeCancel()
			if err := closeErr; err != nil {
				commandError(env, fmt.Errorf("%w; retaining bundle lock until process exit", err))
				code = 1
				return
			}
		}
		if err := lock.Release(); err != nil {
			commandError(env, err)
			code = 1
		}
	}()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *timeout != 0 {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithTimeout(ctx, *timeout)
		defer deadlineCancel()
	}
	session, err = ios.OpenSession(ctx, dir, *initialize, uint16(*debugPort), env.Stdout)
	if err != nil {
		return commandError(env, err)
	}
	fmt.Fprintf(env.Stderr, "ios ecid: 0x%016x\n", session.ECID())
	startCtx, startCancel := context.WithTimeout(ctx, 2*time.Minute)
	err = session.Start(startCtx, ios.StartOptions{ForceDFU: *forceDFU, StopInIBootStage1: *stop1, StopInIBootStage2: *stop2})
	startCancel()
	if err != nil {
		return commandError(env, err)
	}
	if err := session.Wait(ctx); err != nil && ctx.Err() == nil {
		return commandError(env, err)
	}
	return 0
}

func runIOSConfig(env commandEnv, args []string) int {
	flags := flag.NewFlagSet("ios config", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	cpu := flags.Uint("cpu", 0, "virtual CPU count")
	memory := flags.Uint64("memory", 0, "memory in GiB")
	width := flags.Int("width", 0, "display width in pixels")
	height := flags.Int("height", 0, "display height in pixels")
	ppi := flags.Int("ppi", 0, "display pixels per inch")
	scale := flags.Float64("scale", 0, "display scale")
	network := flags.String("network", "", "nat, none, or bridged:interface")
	mac := flags.String("mac", "", "unicast MAC address")
	rom := flags.String("rom", "", "AP ROM path, absolute or relative to the bundle")
	sepROM := flags.String("sep-rom", "", "SEP ROM path, absolute or relative to the bundle")
	noVphoned := flags.Bool("no-vphoned", false, "disable the guest control socket")
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(append([]string{}, args[1:]...), args[0])
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return commandUsageError(env, fmt.Errorf("usage: cove ios config [flags] NAME"))
	}
	name := flags.Arg(0)
	if err := validateNewVMName(name); err != nil {
		return commandUsageError(env, err)
	}
	dir, ok := vmconfig.ExistingPath(name)
	if !ok {
		return commandError(env, fmt.Errorf("vm not found: %s", name))
	}
	if flags.NFlag() != 0 {
		lock, err := AcquireRunLock(dir)
		if err != nil {
			return commandError(env, err)
		}
		defer lock.Release()
	}
	config, err := vmconfig.Load(dir)
	if err != nil {
		return commandError(env, err)
	}
	if config.IOS == nil {
		return commandError(env, fmt.Errorf("vm is not an ios guest: %s", name))
	}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "cpu":
			config.CPU = *cpu
		case "memory":
			config.MemoryGB = *memory
		case "width":
			config.IOS.Display.Width = *width
		case "height":
			config.IOS.Display.Height = *height
		case "ppi":
			config.IOS.Display.PPI = *ppi
		case "scale":
			config.IOS.Display.Scale = *scale
		case "network":
			config.IOS.Network = *network
		case "mac":
			config.IOS.MAC = *mac
		case "rom":
			config.IOS.ROM = *rom
		case "sep-rom":
			config.IOS.SEPROM = *sepROM
		case "no-vphoned":
			config.IOS.NoVphoned = *noVphoned
		}
	})
	if flags.NFlag() != 0 {
		if err := ios.Configure(dir, vmconfig.Hardware{CPU: config.CPU, MemoryGB: config.MemoryGB}, *config.IOS); err != nil {
			return commandError(env, err)
		}
		config, err = vmconfig.Load(dir)
		if err != nil {
			return commandError(env, err)
		}
	}
	if err := json.NewEncoder(env.Stdout).Encode(config); err != nil {
		return commandError(env, err)
	}
	return 0
}

func runIOSSetup(env commandEnv, args []string) int {
	if len(args) > 0 && args[0] == "patcher" {
		return runIOSSetupPatcher(env, args[1:])
	}
	if len(args) == 0 || args[0] != "source" {
		return commandUsageError(env, fmt.Errorf("usage: cove ios setup source [-repository PATH_OR_URL] [-dir PATH] [-check]"))
	}
	flags := flag.NewFlagSet("ios setup source", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	repository := flags.String("repository", "", "upstream URL or local Git checkout")
	directory := flags.String("dir", filepath.Join(vmconfig.StateDir(), "ios", "toolchains", firmware.SourceCommit), "toolchain source cache")
	check := flags.Bool("check", false, "inspect existing sources without downloading")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		return commandUsageError(env, fmt.Errorf("unexpected setup arguments"))
	}
	dir, err := filepath.Abs(*directory)
	if err != nil {
		return commandError(env, err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(ctx, 15*time.Minute)
	defer timeoutCancel()
	var report firmware.Source
	if *check {
		report, err = firmware.InspectSource(ctx, filepath.Join(dir, "source"))
	} else {
		fmt.Fprintln(env.Stderr, "preparing pinned toolchain sources:", dir)
		report, err = firmware.PrepareSource(ctx, dir, *repository)
	}
	if err != nil {
		return commandError(env, err)
	}
	if err := json.NewEncoder(env.Stdout).Encode(report); err != nil {
		return commandError(env, err)
	}
	if len(report.Problems) != 0 {
		return 1
	}
	return 0
}

func runIOSDevices(env commandEnv, args []string) int {
	flags := flag.NewFlagSet("ios devices", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	libusb := flags.String("libusb", "", "libusb shared library for raw DFU/recovery enumeration")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		return commandUsageError(env, fmt.Errorf("unexpected device arguments"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report := ios.DiscoverDevices(ctx, *libusb)
	if err := json.NewEncoder(env.Stdout).Encode(report); err != nil {
		return commandError(env, err)
	}
	if len(report.Errors) != 0 {
		return 1
	}
	return 0
}

func runIOSFirmware(env commandEnv, args []string) int {
	if len(args) == 0 || args[0] != "prepare" {
		return commandUsageError(env, fmt.Errorf("usage: cove ios firmware prepare -source PATH -iphone IPSW -cloudos IPSW OUTPUT"))
	}
	flags := flag.NewFlagSet("ios firmware prepare", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	source := flags.String("source", "", "pinned toolchain source directory")
	iphone := flags.String("iphone", "", "local iPhone IPSW")
	cloudos := flags.String("cloudos", "", "local cloudOS IPSW")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 1 || *source == "" || *iphone == "" || *cloudos == "" {
		return commandUsageError(env, fmt.Errorf("source, iphone, cloudos and output paths are required"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := firmware.Prepare(ctx, *source, *iphone, *cloudos, flags.Arg(0), env.Stderr); err != nil {
		return commandError(env, err)
	}
	fmt.Fprintln(env.Stdout, filepath.Join(flags.Arg(0), "firmware.json"))
	return 0
}

func runIOSSetupPatcher(env commandEnv, args []string) int {
	flags := flag.NewFlagSet("ios setup patcher", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	dir := flags.String("dir", filepath.Join(vmconfig.StateDir(), "ios", "toolchains", firmware.SourceCommit), "toolchain cache containing source")
	developer := flags.String("developer-dir", "", "Xcode developer directory for this build")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		return commandUsageError(env, fmt.Errorf("unexpected patcher setup arguments"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report, err := firmware.BuildPatcher(ctx, *dir, *developer, env.Stderr)
	if err != nil {
		return commandError(env, err)
	}
	if err := json.NewEncoder(env.Stdout).Encode(report); err != nil {
		return commandError(env, err)
	}
	return 0
}
