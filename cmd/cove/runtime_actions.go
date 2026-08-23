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
	"github.com/tmc/cove/internal/vmrun"
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

// restartVM stops the VM and starts it again. The stop half is a real VM stop,
// which the run-loop state monitors would otherwise read as the VM having
// exited, so the whole sequence runs inside a boot transition.
func restartVM(source string, vm vz.VZVirtualMachine, queue dispatch.Queue) {
	label := actionSourceLabel(source)
	fmt.Printf("%s: restarting VM...\n", label)
	beginVMBootTransition()
	go func() {
		defer endVMBootTransition()
		if err := stopVMForBootTransition(label, vm, queue); err != nil {
			reportBootTransitionFailure(label, "vm stop during restart", vm, queue, err)
			return
		}
		fmt.Printf("%s: VM stopped, starting again...\n", label)
		setActiveBootSessionMode(bootSessionModeNormal)
		err := startVMAfterStop(vm, queue, func(handler func(error)) {
			vm.StartWithCompletionHandler(handler)
		})
		if err != nil {
			reportBootTransitionFailure(label, "vm start during restart", vm, queue, err)
			return
		}
		fmt.Printf("%s: VM restarted\n", label)
	}()
}

func bootVMToRecovery(source string, vm vz.VZVirtualMachine, queue dispatch.Queue, vmDirectory string) {
	label := actionSourceLabel(source)
	fmt.Printf("%s: booting to recovery mode...\n", label)
	beginVMBootTransition()
	go func() {
		defer endVMBootTransition()
		if err := stopVMForBootTransition(label, vm, queue); err != nil {
			reportBootTransitionFailure(label, "vm stop before recovery", vm, queue, err)
			return
		}
		if hasSuspendStateForVM(vmDirectory) {
			fmt.Printf("%s: recovery mode requires a cold boot; moving aside saved suspend state...\n", label)
			moveAsideSuspendStateForVM(vmDirectory, "recovery-mode")
		}
		setActiveBootSessionMode(bootSessionModeRecovery)
		err := startVMAfterStop(vm, queue, func(handler func(error)) {
			opts := vz.NewVZMacOSVirtualMachineStartOptions()
			opts.SetStartUpFromMacOSRecovery(true)
			vm.StartWithOptionsCompletionHandler(&opts.VZVirtualMachineStartOptions, handler)
		})
		if err != nil {
			reportBootTransitionFailure(label, "vm recovery start", vm, queue, err)
			return
		}
		fmt.Printf("%s: VM started in recovery mode\n", label)
	}()
}

func requestVMSuspend(source string, vm vz.VZVirtualMachine, queue dispatch.Queue, rc vmrun.RunConfig, hc vmrun.HostConfig) {
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
		if err := suspendVM(vm, queue, rc, hc); err != nil {
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
