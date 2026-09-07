package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tmc/apple/x/irecovery"
	"github.com/tmc/apple/x/usbmux"
	"github.com/tmc/cove/internal/ios/restore"
)

func runIOSRestore(env commandEnv, args []string) int {
	if len(args) > 0 && args[0] == "recovery-probe" {
		return runIOSRecoveryProbe(env, args[1:])
	}
	if len(args) == 0 || args[0] != "probe" {
		return commandUsageError(env, fmt.Errorf("usage: cove ios restore probe -ecid N [-udid SERIAL] | recovery-probe -ecid N -libusb PATH"))
	}
	flags := flag.NewFlagSet("ios restore probe", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	ecid := flags.Uint64("ecid", 0, "required hardware ECID (decimal or 0x-prefixed)")
	serial := flags.String("udid", "", "optional usbmux serial filter")
	timeout := flags.Duration("timeout", 15*time.Second, "discovery timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *ecid == 0 || *timeout <= 0 {
		return commandUsageError(env, fmt.Errorf("nonzero ECID and positive timeout are required"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	device, err := restore.Probe(ctx, usbmux.Client{}, restore.Target{ECID: *ecid, Serial: *serial})
	if err != nil {
		return commandError(env, err)
	}
	if err := json.NewEncoder(env.Stdout).Encode(device); err != nil {
		return commandError(env, err)
	}
	return 0
}

func runIOSRecoveryProbe(env commandEnv, args []string) int {
	flags := flag.NewFlagSet("ios restore recovery-probe", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	ecid := flags.Uint64("ecid", 0, "required hardware ECID (decimal or 0x-prefixed)")
	library := flags.String("libusb", "", "required libusb shared library path")
	timeout := flags.Duration("timeout", 15*time.Second, "observation timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *ecid == 0 || *library == "" || *timeout <= 0 {
		return commandUsageError(env, fmt.Errorf("nonzero ECID, libusb path and positive timeout are required"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	conn, err := irecovery.Open(ctx, *library, *ecid)
	if err != nil {
		return commandError(env, err)
	}
	device, err := restore.ObserveRecovery(ctx, conn, *ecid)
	err = errors.Join(err, conn.Close())
	if err != nil {
		return commandError(env, err)
	}
	if err := json.NewEncoder(env.Stdout).Encode(device); err != nil {
		return commandError(env, err)
	}
	return 0
}
