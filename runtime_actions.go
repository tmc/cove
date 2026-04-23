package main

import (
	"fmt"
	"image/png"
	"os"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/uniformtypeidentifiers"
	vz "github.com/tmc/apple/virtualization"
)

func actionSourceLabel(source string) string {
	if source == "" {
		return "VM"
	}
	return source
}

func requestVMStop(source string, vm vz.VZVirtualMachine, queue dispatch.Queue) {
	label := actionSourceLabel(source)
	fmt.Printf("%s: requesting VM stop...\n", label)
	DispatchAsyncQueue(queue, func() {
		ok, err := vm.RequestStopWithError()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: vm stop request: %v\n", err)
			return
		}
		if ok {
			return
		}
		fmt.Printf("%s: VM stop request returned false, forcing stop...\n", label)
		vm.StopWithCompletionHandler(func(err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: vm force stop: %v\n", err)
			}
		})
	})
}

func toggleVMStartPause(source string, vm vz.VZVirtualMachine, queue dispatch.Queue) {
	label := actionSourceLabel(source)
	DispatchAsyncQueue(queue, func() {
		switch state := vz.VZVirtualMachineState(vm.State()); state {
		case vz.VZVirtualMachineStateRunning:
			fmt.Printf("%s: pausing VM...\n", label)
			vm.PauseWithCompletionHandler(func(err error) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: vm pause: %v\n", err)
				}
			})
		case vz.VZVirtualMachineStatePaused:
			fmt.Printf("%s: resuming VM...\n", label)
			setActiveBootSessionMode(bootSessionModeNormal)
			vm.ResumeWithCompletionHandler(func(err error) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: vm resume: %v\n", err)
				}
			})
		case vz.VZVirtualMachineStateStopped:
			fmt.Printf("%s: starting VM...\n", label)
			setActiveBootSessionMode(bootSessionModeNormal)
			vm.StartWithCompletionHandler(func(err error) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: vm start: %v\n", err)
				}
			})
		}
	})
}

func restartVM(source string, vm vz.VZVirtualMachine, queue dispatch.Queue) {
	label := actionSourceLabel(source)
	fmt.Printf("%s: restarting VM...\n", label)
	DispatchAsyncQueue(queue, func() {
		vm.StopWithCompletionHandler(func(err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: vm stop during restart: %v\n", err)
				return
			}
			fmt.Printf("%s: VM stopped, starting again...\n", label)
			setActiveBootSessionMode(bootSessionModeNormal)
			vm.StartWithCompletionHandler(func(err error) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: vm start during restart: %v\n", err)
				}
			})
		})
	})
}

func bootVMToRecovery(source string, vm vz.VZVirtualMachine, queue dispatch.Queue) {
	label := actionSourceLabel(source)
	fmt.Printf("%s: booting to recovery mode...\n", label)
	DispatchAsyncQueue(queue, func() {
		startRecovery := func() {
			if hasSuspendState() {
				fmt.Printf("%s: recovery mode requires a cold boot; moving aside saved suspend state...\n", label)
				moveAsideSuspendState("recovery-mode")
			}
			setActiveBootSessionMode(bootSessionModeRecovery)
			opts := vz.NewVZMacOSVirtualMachineStartOptions()
			opts.SetStartUpFromMacOSRecovery(true)
			vm.StartWithOptionsCompletionHandler(
				&opts.VZVirtualMachineStartOptions,
				func(err error) {
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: vm recovery start: %v\n", err)
						return
					}
					fmt.Printf("%s: VM started in recovery mode\n", label)
				},
			)
		}

		if vz.VZVirtualMachineState(vm.State()) == vz.VZVirtualMachineStateStopped {
			startRecovery()
			return
		}
		vm.StopWithCompletionHandler(func(err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: vm stop before recovery: %v\n", err)
				return
			}
			startRecovery()
		})
	})
}

func requestVMSuspend(source string, vm vz.VZVirtualMachine, queue dispatch.Queue) {
	label := actionSourceLabel(source)
	if !canSaveRestore {
		fmt.Printf("%s: save/restore not supported for this VM configuration\n", label)
		return
	}
	if !activeBootSessionAllowsSuspend() {
		fmt.Printf("%s: suspend unavailable while running in %s mode\n", label, bootSessionModeString(currentBootSessionMode()))
		return
	}
	fmt.Printf("%s: suspending VM...\n", label)
	go func() {
		if err := suspendVM(vm, queue); err != nil {
			fmt.Fprintf(os.Stderr, "error: suspend: %v\n", err)
			return
		}
		fmt.Printf("%s: VM suspended (will resume on next launch)\n", label)
	}()
}

func saveCurrentVMScreenshot(source string, provider vmScreenshotProvider) {
	label := actionSourceLabel(source)
	if provider == nil {
		fmt.Printf("%s: screenshot unavailable\n", label)
		return
	}

	img, errMsg := provider.captureDisplayImage()
	if errMsg != "" {
		fmt.Fprintf(os.Stderr, "error: screenshot: %s\n", errMsg)
		return
	}

	panel := appkit.NewNSSavePanel()
	defaultName := fmt.Sprintf("cove_%s.png", time.Now().Format("20060102_150405"))
	panel.SetNameFieldStringValue(defaultName)
	panel.SetMessage("Save VM Screenshot")
	pngType := uniformtypeidentifiers.NewTypeWithFilenameExtension("png")
	if pngType.ID != 0 {
		panel.SetAllowedContentTypes([]uniformtypeidentifiers.UTType{pngType})
	}

	response := panel.RunModal()
	if !isModalResponseOK(response) {
		return
	}

	url := panel.URL()
	if url.GetID() == 0 {
		return
	}
	savePath := url.Path()

	f, err := os.Create(savePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: screenshot save: %v\n", err)
		return
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		fmt.Fprintf(os.Stderr, "error: screenshot encode: %v\n", err)
		return
	}
	fmt.Printf("%s: screenshot saved to %s\n", label, savePath)
}
