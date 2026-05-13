//go:build darwin && arm64

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	privvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
)

var (
	flagIntegration = flag.Bool("integration", false, "run integration tests against a live VM")
	flagTestVM      = flag.String("test-vm", "cove-test", "VM name to use for integration tests")
)

// liveVM holds a running VM for integration tests. Initialized once via sync.Once.
var (
	liveVMOnce  sync.Once
	liveVMPub   vz.VZVirtualMachine
	liveVMPriv  privvz.VZVirtualMachine
	liveVMQueue dispatch.Queue
	liveVMErr   error
)

// requireLiveVM returns a running VM for testing. Skips if -integration not set.
func requireLiveVM(t *testing.T) (vz.VZVirtualMachine, privvz.VZVirtualMachine, dispatch.Queue) {
	t.Helper()
	if !*flagIntegration {
		t.Skip("skipping: pass -integration to run live VM tests")
	}

	liveVMOnce.Do(func() {
		liveVMPub, liveVMPriv, liveVMQueue, liveVMErr = startLiveVM(*flagTestVM)
	})

	if liveVMErr != nil {
		t.Fatalf("live VM startup failed: %v", liveVMErr)
	}
	return liveVMPub, liveVMPriv, liveVMQueue
}

func startLiveVM(vmName string) (vz.VZVirtualMachine, privvz.VZVirtualMachine, dispatch.Queue, error) {
	home, _ := os.UserHomeDir()
	vmPath := filepath.Join(home, ".vz", "vms", vmName)

	diskPath := filepath.Join(vmPath, "disk.img")
	auxPath := filepath.Join(vmPath, "aux.img")
	hwModelPath := filepath.Join(vmPath, "hw.model")
	machineIDPath := filepath.Join(vmPath, "machine.id")

	for _, p := range []string{diskPath, auxPath, hwModelPath, machineIDPath} {
		if _, err := os.Stat(p); err != nil {
			return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{},
				fmt.Errorf("VM %q file missing: %s", vmName, filepath.Base(p))
		}
	}

	// Load hardware model
	hwModelData, err := os.ReadFile(hwModelPath)
	if err != nil {
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("read hw.model: %w", err)
	}
	hwModelNSData := foundation.NewDataWithBytesLength(hwModelData)
	hwModel := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacHardwareModel")), objc.Sel("alloc")),
		objc.Sel("initWithDataRepresentation:"), hwModelNSData.ID,
	)
	if hwModel == 0 {
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("failed to create VZMacHardwareModel")
	}

	// Load machine identifier
	machineIDData, err := os.ReadFile(machineIDPath)
	if err != nil {
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("read machine.id: %w", err)
	}
	machineIDNSData := foundation.NewDataWithBytesLength(machineIDData)
	machineID := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacMachineIdentifier")), objc.Sel("alloc")),
		objc.Sel("initWithDataRepresentation:"), machineIDNSData.ID,
	)
	if machineID == 0 {
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("failed to create VZMacMachineIdentifier")
	}

	// Auxiliary storage
	auxURL := objc.Send[objc.ID](objc.ID(objc.GetClass("NSURL")), objc.Sel("fileURLWithPath:"), objc.String(auxPath))
	auxStorage := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacAuxiliaryStorage")), objc.Sel("alloc")),
		objc.Sel("initWithURL:"), auxURL,
	)

	// Platform config
	platform := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacPlatformConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](platform, objc.Sel("setHardwareModel:"), hwModel)
	objc.Send[objc.ID](platform, objc.Sel("setMachineIdentifier:"), machineID)
	objc.Send[objc.ID](platform, objc.Sel("setAuxiliaryStorage:"), auxStorage)

	// Boot loader
	bootLoader := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacOSBootLoader")), objc.Sel("alloc")),
		objc.Sel("init"),
	)

	// Disk attachment (read-write for boot)
	diskURL := objc.Send[objc.ID](objc.ID(objc.GetClass("NSURL")), objc.Sel("fileURLWithPath:"), objc.String(diskPath))
	var diskErrPtr objc.ID
	diskAttachment := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZDiskImageStorageDeviceAttachment")), objc.Sel("alloc")),
		objc.Sel("initWithURL:readOnly:cachingMode:synchronizationMode:error:"),
		diskURL, false, /* readOnly=false for boot */
		int64(0), /* automatic caching */
		int64(1), /* full sync */
		&diskErrPtr,
	)
	if diskErrPtr != 0 {
		errMsg := foundation.NSStringFromID(objc.Send[objc.ID](diskErrPtr, objc.Sel("localizedDescription"))).String()
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("disk attachment: %s", errMsg)
	}

	storageConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtioBlockDeviceConfiguration")), objc.Sel("alloc")),
		objc.Sel("initWithAttachment:"), diskAttachment,
	)

	// Audio (required for macOS VM)
	audioConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtioSoundDeviceConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	inputStream := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtioSoundDeviceInputStreamConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	hostInput := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZHostAudioInputStreamSource")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](inputStream, objc.Sel("setSource:"), hostInput)

	outputStream := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtioSoundDeviceOutputStreamConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	hostOutput := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZHostAudioOutputStreamSink")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](outputStream, objc.Sel("setSink:"), hostOutput)

	streams := [2]objc.ID{inputStream, outputStream}
	streamsArray := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")),
		objc.Sel("arrayWithObjects:count:"),
		unsafe.Pointer(&streams),
		uint(2),
	)
	objc.Send[objc.ID](audioConfig, objc.Sel("setStreams:"), streamsArray)

	// Graphics
	graphicsConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacGraphicsDeviceConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	displayConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacGraphicsDisplayConfiguration")), objc.Sel("alloc")),
		objc.Sel("initWithWidthInPixels:heightInPixels:pixelsPerInch:"),
		int64(1920), int64(1200), int64(144),
	)
	displaysArray := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")), objc.Sel("arrayWithObject:"), displayConfig,
	)
	objc.Send[objc.ID](graphicsConfig, objc.Sel("setDisplays:"), displaysArray)

	// Network
	networkConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtioNetworkDeviceConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	natAttachment := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZNATNetworkDeviceAttachment")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](networkConfig, objc.Sel("setAttachment:"), natAttachment)

	// Keyboard + trackpad
	kbConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacKeyboardConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	trackpadConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacTrackpadConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)

	// Assemble VM configuration
	config := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtualMachineConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](config, objc.Sel("setPlatform:"), platform)
	objc.Send[objc.ID](config, objc.Sel("setBootLoader:"), bootLoader)
	objc.Send[objc.ID](config, objc.Sel("setCPUCount:"), uint64(2))
	objc.Send[objc.ID](config, objc.Sel("setMemorySize:"), uint64(4*1024*1024*1024))

	setArray := func(sel string, obj objc.ID) {
		arr := objc.Send[objc.ID](objc.ID(objc.GetClass("NSArray")), objc.Sel("arrayWithObject:"), obj)
		objc.Send[objc.ID](config, objc.Sel(sel), arr)
	}
	setArray("setStorageDevices:", storageConfig)
	setArray("setAudioDevices:", audioConfig)
	setArray("setGraphicsDevices:", graphicsConfig)
	setArray("setNetworkDevices:", networkConfig)
	setArray("setKeyboards:", kbConfig)
	setArray("setPointingDevices:", trackpadConfig)

	// Validate
	var validateErrPtr objc.ID
	valid := objc.Send[bool](config, objc.Sel("validateWithError:"), &validateErrPtr)
	if !valid {
		errMsg := "(nil)"
		if validateErrPtr != 0 {
			errMsg = foundation.NSStringFromID(objc.Send[objc.ID](validateErrPtr, objc.Sel("localizedDescription"))).String()
		}
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("config validation: %s", errMsg)
	}

	// Create VM
	queue := dispatch.QueueCreate("com.test.private-api-live")
	vmInstance := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtualMachine")), objc.Sel("alloc")),
		objc.Sel("initWithConfiguration:queue:"), config, queue.Handle(),
	)
	if vmInstance == 0 {
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("failed to create VZVirtualMachine")
	}
	objc.Send[objc.ID](vmInstance, objc.Sel("retain"))

	pubVM := vz.VZVirtualMachine{Object: objectivec.Object{ID: vmInstance}}
	privVM := privvz.VZVirtualMachineFromID(vmInstance)

	// Start
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	startErr := make(chan error, 1)
	queue.Sync(func() {
		pubVM.StartWithCompletionHandler(func(err error) {
			startErr <- err
		})
	})
	select {
	case err := <-startErr:
		if err != nil {
			return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("start VM: %w", err)
		}
	case <-ctx.Done():
		return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("start VM: timeout")
	}

	// Poll for running state
	for i := 0; i < 300; i++ {
		var state int64
		done := make(chan struct{})
		queue.Sync(func() {
			state = int64(pubVM.State())
			close(done)
		})
		<-done
		if state == 1 { // Running
			break
		}
		if state == 6 { // Error
			return vz.VZVirtualMachine{}, privvz.VZVirtualMachine{}, dispatch.Queue{}, fmt.Errorf("VM entered error state")
		}
		time.Sleep(100 * time.Millisecond)
	}

	return pubVM, privVM, queue, nil
}

// ============================================================================
// Integration Tests: require -integration flag and a VM named "cove-test"
// ============================================================================

// A. _stateDescription on running VM
func TestLiveAPI_StateDescription(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	var desc string
	queue.Sync(func() {
		descID := objc.Send[objc.ID](privVM.ID, objc.Sel("_stateDescription"))
		desc = foundation.NSStringFromID(descID).String()
	})
	t.Logf("_stateDescription (running): %q", desc)
}

// A. _stateDescription on paused VM
func TestLiveAPI_StateDescriptionPaused(t *testing.T) {
	pubVM, privVM, queue := requireLiveVM(t)

	var canPause bool
	queue.Sync(func() { canPause = pubVM.CanPause() })
	if !canPause {
		t.Skip("VM cannot be paused in current state")
	}

	pauseErr := make(chan error, 1)
	queue.Sync(func() {
		pubVM.PauseWithCompletionHandler(func(err error) { pauseErr <- err })
	})
	if err := <-pauseErr; err != nil {
		t.Fatalf("pause: %v", err)
	}

	var desc string
	queue.Sync(func() {
		descID := objc.Send[objc.ID](privVM.ID, objc.Sel("_stateDescription"))
		desc = foundation.NSStringFromID(descID).String()
	})
	t.Logf("_stateDescription (paused): %q", desc)

	resumeErr := make(chan error, 1)
	queue.Sync(func() {
		pubVM.ResumeWithCompletionHandler(func(err error) { resumeErr <- err })
	})
	if err := <-resumeErr; err != nil {
		t.Fatalf("resume: %v", err)
	}
}

// B. _resetWithType on running VM
func TestLiveAPI_ResetWithType(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	done := make(chan error, 1)
	queue.Sync(func() {
		privVM.ResetWithTypeCompletionHandler(0, func(err error) {
			done <- err
		})
	})
	select {
	case err := <-done:
		if err != nil {
			t.Logf("_resetWithType(0): error: %v", err)
		} else {
			t.Log("_resetWithType(0): success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("_resetWithType(0): timeout")
	}
}

// C. _saveMachineStateToURL on running VM (must pause first)
func TestLiveAPI_SaveMachineState(t *testing.T) {
	pubVM, privVM, queue := requireLiveVM(t)

	// Pause the VM first — save requires paused state
	var canPause bool
	queue.Sync(func() { canPause = pubVM.CanPause() })
	if !canPause {
		t.Skip("VM cannot be paused")
	}

	pauseErr := make(chan error, 1)
	queue.Sync(func() {
		pubVM.PauseWithCompletionHandler(func(err error) { pauseErr <- err })
	})
	if err := <-pauseErr; err != nil {
		t.Fatalf("pause: %v", err)
	}
	defer func() {
		resumeErr := make(chan error, 1)
		queue.Sync(func() {
			pubVM.ResumeWithCompletionHandler(func(err error) { resumeErr <- err })
		})
		<-resumeErr
	}()

	tmpDir := t.TempDir()
	savePath := filepath.Join(tmpDir, "test.vmstate")
	saveURL := foundation.NewURLFileURLWithPath(savePath)

	done := make(chan error, 1)
	queue.Sync(func() {
		privVM.SaveMachineStateToURLOptionsCompletionHandler(saveURL, nil, func(err error) {
			done <- err
		})
	})
	select {
	case err := <-done:
		if err != nil {
			t.Logf("_saveMachineStateToURL: error: %v", err)
		} else {
			t.Log("_saveMachineStateToURL: success")
			info, _ := os.Stat(savePath)
			if info != nil {
				t.Logf("  file size: %d bytes", info.Size())
			}
		}
	case <-time.After(30 * time.Second):
		t.Fatal("_saveMachineStateToURL: timeout")
	}
}

// D. SendPointerNSEvent probing
func TestLiveAPI_SendPointerNSEvent(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	queue.Sync(func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("recovered panic: %v", r)
			}
		}()

		responds := objc.Send[bool](privVM.ID, objc.Sel("respondsToSelector:"), objc.Sel("sendPointerNSEvent:pointingDeviceIndex:"))
		t.Logf("responds to sendPointerNSEvent:pointingDeviceIndex: = %v", responds)

		pointingDevices := objc.Send[objc.ID](privVM.ID, objc.Sel("_pointingDevices"))
		if pointingDevices != 0 {
			count := objc.Send[int64](pointingDevices, objc.Sel("count"))
			t.Logf("pointing devices count: %d", count)
			for i := int64(0); i < count; i++ {
				elem := objc.Send[objc.ID](pointingDevices, objc.Sel("objectAtIndex:"), uint(i))
				className := foundation.NSStringFromID(objc.Send[objc.ID](elem, objc.Sel("className"))).String()
				t.Logf("  pointing device[%d]: %s", i, className)
			}
		}
	})
}

// E. _keyboards discovery
func TestLiveAPI_KeyboardsDiscovery(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	queue.Sync(func() {
		keyboards := objc.Send[objc.ID](privVM.ID, objc.Sel("_keyboards"))
		if keyboards == 0 {
			t.Log("_keyboards: nil")
			return
		}
		count := objc.Send[int64](keyboards, objc.Sel("count"))
		t.Logf("_keyboards count: %d", count)

		for i := int64(0); i < count; i++ {
			kb := objc.Send[objc.ID](keyboards, objc.Sel("objectAtIndex:"), uint(i))
			className := foundation.NSStringFromID(objc.Send[objc.ID](kb, objc.Sel("className"))).String()
			desc := foundation.NSStringFromID(objc.Send[objc.ID](kb, objc.Sel("description"))).String()
			t.Logf("  keyboard[%d]: class=%s desc=%s", i, className, desc)
		}
	})
}

// F. _takeScreenshot on running VM
func TestLiveAPI_TakeScreenshot(t *testing.T) {
	pubVM, _, queue := requireLiveVM(t)

	done := make(chan error, 1)
	queue.Sync(func() {
		gfxDevices := pubVM.GraphicsDevices()
		if len(gfxDevices) == 0 {
			t.Skip("no graphics devices")
			return
		}
		displays := gfxDevices[0].Displays()
		if len(displays) == 0 {
			t.Skip("no displays")
			return
		}

		privDisplay := privvz.VZGraphicsDisplayFromID(displays[0].ID)
		privDisplay.TakeScreenshotWithCompletionHandler(func(err error) {
			done <- err
		})
	})

	select {
	case err := <-done:
		if err != nil {
			t.Logf("_takeScreenshot: error: %v", err)
		} else {
			t.Log("_takeScreenshot: success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("_takeScreenshot: timeout")
	}
}

// G. SizeInPixels on running VM (public API, crashes on stopped VMs)
func TestLiveAPI_SizeInPixels(t *testing.T) {
	pubVM, _, queue := requireLiveVM(t)

	queue.Sync(func() {
		gfxDevices := pubVM.GraphicsDevices()
		if len(gfxDevices) == 0 {
			t.Skip("no graphics devices")
			return
		}
		displays := gfxDevices[0].Displays()
		if len(displays) == 0 {
			t.Skip("no displays")
			return
		}

		// Use public VZGraphicsDisplay which has SizeInPixels
		display := displays[0]
		size := display.SizeInPixels()
		t.Logf("SizeInPixels: width=%.0f height=%.0f", size.Width, size.Height)
	})
}

// H. _createViewEndpoint
func TestLiveAPI_CreateViewEndpoint(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	for _, opt := range []uint64{0, 1, 2} {
		t.Run(fmt.Sprintf("options=%d", opt), func(t *testing.T) {
			queue.Sync(func() {
				defer func() {
					if r := recover(); r != nil {
						t.Logf("options=%d: panic=%v", opt, r)
					}
				}()

				endpoint, err := privVM.CreateViewEndpointWithOptions(opt)
				if err != nil {
					t.Logf("options=%d: error=%v", opt, err)
					return
				}
				if endpoint == nil {
					t.Logf("options=%d: returned nil", opt)
					return
				}
				obj, ok := endpoint.(objectivec.Object)
				if ok && obj.ID != 0 {
					className := foundation.NSStringFromID(objc.Send[objc.ID](obj.ID, objc.Sel("className"))).String()
					t.Logf("options=%d: returned %s (ID=%v)", opt, className, obj.ID)
				} else {
					t.Logf("options=%d: returned non-nil but couldn't cast to Object", opt)
				}
			})
		})
	}
}

// I. _enterRestrictedMode — validate + attempt enter
func TestLiveAPI_RestrictedMode(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	var supported bool
	var valErr error
	queue.Sync(func() {
		supported, valErr = privVM.ValidateRestrictedModeSupportWithError()
	})
	t.Logf("restricted mode supported: %v, error: %v", supported, valErr)

	done := make(chan error, 1)
	queue.Sync(func() {
		privVM.EnterRestrictedModeWithCompletionHandler(func(err error) {
			done <- err
		})
	})
	select {
	case err := <-done:
		if err != nil {
			t.Logf("_enterRestrictedMode: error: %v", err)
		} else {
			t.Log("_enterRestrictedMode: success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("_enterRestrictedMode: timeout")
	}
}

// K. _currentConfiguration on running VM
func TestLiveAPI_CurrentConfiguration(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	queue.Sync(func() {
		configID := objc.Send[objc.ID](privVM.ID, objc.Sel("_currentConfiguration"))
		if configID == 0 {
			t.Log("_currentConfiguration: nil")
			return
		}

		className := foundation.NSStringFromID(objc.Send[objc.ID](configID, objc.Sel("className"))).String()
		t.Logf("_currentConfiguration: class=%s objc.ID=%v", className, configID)

		// Use public selectors (uppercase CPUCount, not cpuCount from private binding)
		cpuCount := objc.Send[uint64](configID, objc.Sel("CPUCount"))
		memSize := objc.Send[uint64](configID, objc.Sel("memorySize"))
		t.Logf("  CPUCount: %d", cpuCount)
		t.Logf("  memorySize: %d bytes (%.1f GB)", memSize, float64(memSize)/(1024*1024*1024))

		// Private properties via raw objc.Send
		t.Logf("  _memoryOvercommitmentAllowed: %v",
			objc.Send[bool](configID, objc.Sel("_memoryOvercommitmentAllowed")))
		t.Logf("  _fatalErrorAction: %d",
			objc.Send[int64](configID, objc.Sel("_fatalErrorAction")))
		t.Logf("  _panicAction: %d",
			objc.Send[int64](configID, objc.Sel("_panicAction")))
		t.Logf("  _restartAction: %d",
			objc.Send[int64](configID, objc.Sel("_restartAction")))
	})
}

// Diagnostic properties on running VM
func TestLiveAPI_DiagnosticProperties(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	queue.Sync(func() {
		stateDesc := objc.Send[objc.ID](privVM.ID, objc.Sel("_stateDescription"))
		t.Logf("_stateDescription: %q", foundation.NSStringFromID(stateDesc).String())

		t.Logf("state: %d", privVM.State())

		canCreate := objc.Send[bool](privVM.ID, objc.Sel("_canCreateCore"))
		t.Logf("_canCreateCore: %v", canCreate)

		hidReports, err := privVM.ShouldSendHIDReports()
		if err != nil {
			t.Logf("_shouldSendHIDReports: error=%v", err)
		} else {
			t.Logf("_shouldSendHIDReports: %v", hidReports)
		}

		pid := objc.Send[int](privVM.ID, objc.Sel("_serviceProcessIdentifier"))
		t.Logf("_serviceProcessIdentifier: %d", pid)

		crashMsg := objc.Send[objc.ID](privVM.ID, objc.Sel("_crashContextMessage"))
		t.Logf("_crashContextMessage: %q", foundation.NSStringFromID(crashMsg).String())

		vmName := objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		t.Logf("_name: %q", foundation.NSStringFromID(vmName).String())
	})
}

// Device arrays on running VM
func TestLiveAPI_DeviceArrays(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	arrays := []struct {
		name string
		sel  string
	}{
		{"audio devices", "_audioDevices"},
		{"keyboards", "_keyboards"},
		{"pointing devices", "_pointingDevices"},
		{"multi-touch devices", "_multiTouchDevices"},
		{"serial ports", "_serialPorts"},
		{"storage devices", "_storageDevices"},
		{"coprocessors", "_coprocessors"},
		{"power source devices", "_powerSourceDevices"},
	}

	queue.Sync(func() {
		for _, a := range arrays {
			arrID := objc.Send[objc.ID](privVM.ID, objc.Sel(a.sel))
			count := int64(0)
			if arrID != 0 {
				count = objc.Send[int64](arrID, objc.Sel("count"))
			}
			t.Logf("%s: count=%d", a.name, count)
			for i := int64(0); i < count; i++ {
				elem := objc.Send[objc.ID](arrID, objc.Sel("objectAtIndex:"), uint(i))
				className := foundation.NSStringFromID(objc.Send[objc.ID](elem, objc.Sel("className"))).String()
				t.Logf("  [%d] %s", i, className)
			}
		}
	})
}

// _debugStub on running VM
func TestLiveAPI_DebugStub(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	queue.Sync(func() {
		stub := objc.Send[objc.ID](privVM.ID, objc.Sel("_debugStub"))
		if stub == 0 {
			t.Log("_debugStub: nil (not configured)")
		} else {
			className := foundation.NSStringFromID(objc.Send[objc.ID](stub, objc.Sel("className"))).String()
			t.Logf("_debugStub: %s (ID=%v)", className, stub)
		}
	})
}

// _hidEventMonitor on running VM
func TestLiveAPI_HIDEventMonitor(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	queue.Sync(func() {
		monitor := objc.Send[objc.ID](privVM.ID, objc.Sel("_hidEventMonitor"))
		if monitor == 0 {
			t.Log("_hidEventMonitor: nil")
		} else {
			className := foundation.NSStringFromID(objc.Send[objc.ID](monitor, objc.Sel("className"))).String()
			t.Logf("_hidEventMonitor: %s (ID=%v)", className, monitor)
		}
	})
}

// Display private properties on running VM
func TestLiveAPI_DisplayProperties(t *testing.T) {
	pubVM, _, queue := requireLiveVM(t)

	queue.Sync(func() {
		gfxDevices := pubVM.GraphicsDevices()
		if len(gfxDevices) == 0 {
			t.Skip("no graphics devices")
			return
		}
		displays := gfxDevices[0].Displays()
		if len(displays) == 0 {
			t.Skip("no displays")
			return
		}

		display := displays[0]
		privDisplay := privvz.VZGraphicsDisplayFromID(display.ID)
		t.Logf("display ID: %v", display.ID)

		// These crashed on stopped VMs — test on running VM
		props := []struct {
			name string
			sel  string
		}{
			{"_connectionType", "_connectionType"},
			{"_displayMode", "_displayMode"},
			{"_displayIdentifier", "_displayIdentifier"},
			{"_uuid", "_uuid"},
			{"_graphicsOrientation", "_graphicsOrientation"},
		}

		for _, p := range props {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Logf("%s: PANIC: %v", p.name, r)
					}
				}()

				result := objc.Send[objc.ID](privDisplay.ID, objc.Sel(p.sel))
				if result == 0 {
					t.Logf("%s: nil/0", p.name)
				} else {
					desc := foundation.NSStringFromID(objc.Send[objc.ID](result, objc.Sel("description"))).String()
					t.Logf("%s: %s", p.name, desc)
				}
			}()
		}
	})
}

// _setName / _name on running VM
func TestLiveAPI_SetName(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	queue.Sync(func() {
		origID := objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		original := foundation.NSStringFromID(origID).String()
		t.Logf("original name: %q", original)

		objc.Send[objc.ID](privVM.ID, objc.Sel("_setName:"), objc.String("test-private-api-live"))
		got := objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		t.Logf("name after set: %q", foundation.NSStringFromID(got).String())

		objc.Send[objc.ID](privVM.ID, objc.Sel("_setName:"), objc.String(original))
		restored := objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		t.Logf("restored name: %q", foundation.NSStringFromID(restored).String())
	})
}
