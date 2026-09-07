// Command lfbprobe reports whether this host can build and run a virtual
// machine carrying the private linear framebuffer graphics device.
//
// The device is gated by com.apple.private.virtualization, a restricted
// entitlement. The probe separates the failures that gate can produce, so a
// run tells you which wall you hit:
//
//	class absent       the framework on this OS has no such class at all
//	config refused     the class exists but the object would not construct
//	validation refused the configuration would not validate with the device
//	create refused     the object validates; the VM service rejected the device
//
// The last is the entitlement gate. Run it before and after any change to how
// the binary is signed or validated; the transition from "create refused" to
// "created" is the only evidence that matters.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/framebuffer"
	platformx "github.com/tmc/apple/x/vzkit/platform"
)

func main() {
	width := flag.Int("width", 1920, "framebuffer width")
	height := flag.Int("height", 1200, "framebuffer height")
	plain := flag.Bool("plain", false, "omit the framebuffer device (baseline: proves the rest of the config is sound)")
	virtio := flag.Bool("virtio", false, "attach the public virtio graphics device instead (control: proves the config accepts a graphics device)")
	start := flag.Bool("start", false, "also start the machine; the device is exercised for real only here")
	flag.Parse()

	if err := run(*width, *height, *plain, *virtio, *start); err != nil {
		fmt.Fprintf(os.Stderr, "\nRESULT: blocked — %v\n", err)
		os.Exit(1)
	}
	fmt.Print("\nRESULT: created — the virtual machine was accepted\n")
}

func run(width, height int, plain, virtio, start bool) error {
	config, err := minimalConfig()
	if err != nil {
		return err
	}

	switch {
	case plain:
		fmt.Println("  baseline run:   no graphics device attached")

	case virtio:
		scanout := vz.NewVirtioGraphicsScanoutConfigurationWithWidthInPixelsHeightInPixels(width, height)
		if scanout.ID == 0 {
			return fmt.Errorf("config refused: virtio scanout")
		}
		graphics := vz.NewVZVirtioGraphicsDeviceConfiguration()
		if graphics.ID == 0 {
			return fmt.Errorf("config refused: virtio graphics device")
		}
		graphics.SetScanouts([]vz.VZVirtioGraphicsScanoutConfiguration{scanout})
		fmt.Printf("  control run:    public virtio graphics, %dx%d scanout\n", width, height)
		objc.Send[struct{}](config.ID, objc.Sel("setGraphicsDevices:"),
			objectivec.IObjectSliceToNSArray([]vz.VZGraphicsDeviceConfiguration{
				vz.VZGraphicsDeviceConfigurationFromID(graphics.ID)}))

	default:
		// Step 1: does the class exist in this OS's Virtualization.framework?
		cls := pvz.GetVZLinearFramebufferGraphicsDeviceConfigurationClass()
		if cls.Class() == 0 {
			return fmt.Errorf("class absent: _VZLinearFramebufferGraphicsDeviceConfiguration is not in this framework")
		}
		fmt.Printf("  class present:  _VZLinearFramebufferGraphicsDeviceConfiguration @ %#x\n", uintptr(cls.Class()))

		// Step 2: construct the device configuration object.
		graphics, err := framebuffer.NewLinearFramebufferGraphicsDeviceConfiguration(
			framebuffer.LinearFramebufferConfig{Width: width, Height: height})
		if err != nil {
			return fmt.Errorf("config refused: %w", err)
		}
		fmt.Printf("  config built:   %dx%d backing store\n", width, height)

		objc.Send[struct{}](config.ID, objc.Sel("setGraphicsDevices:"),
			objectivec.IObjectSliceToNSArray([]vz.VZGraphicsDeviceConfiguration{graphics}))
	}

	// Step 3: validation. The framework checks device shape here, in process.
	ok, err := config.ValidateWithError()
	if !ok {
		return fmt.Errorf("validation refused: %v", err)
	}
	fmt.Println("  config valid:   VZVirtualMachineConfiguration accepted the device")

	// Step 4: the entitlement gate. Creating the machine hands the device list
	// to the VM XPC service, which is where the private-entitlement bit is
	// checked and where an unentitled binary is turned away.
	queue := dispatch.QueueCreate("com.tmc.cove.lfbprobe")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, queue)
	if vm.ID == 0 {
		return fmt.Errorf("create refused: VZVirtualMachine could not be created with this configuration")
	}
	vm.Retain()
	fmt.Printf("  vm created:     VZVirtualMachine @ %#x\n", uintptr(vm.ID))
	if !start {
		return nil
	}

	// Step 5: starting hands the device to the VM service for real. A device
	// the service will not back shows up here and nowhere earlier.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	errc := make(chan error, 1)
	queue.Async(func() { vm.StartWithCompletionHandler(func(err error) { errc <- err }) })
	select {
	case err := <-errc:
		if err != nil {
			return fmt.Errorf("start refused: %w", err)
		}
	case <-ctx.Done():
		return fmt.Errorf("start timed out after 30s")
	}
	fmt.Println("  vm started:     the machine is running with this device")
	return nil
}

// minimalConfig builds the smallest configuration the framework will validate:
// an EFI bootloader with a variable store, one CPU, and 2 GiB of memory.
func minimalConfig() (vz.VZVirtualMachineConfiguration, error) {
	var zero vz.VZVirtualMachineConfiguration
	dir, err := os.MkdirTemp("", "lfbprobe")
	if err != nil {
		return zero, err
	}
	bootloader, _, err := platformx.CreateEFIBootLoader(filepath.Join(dir, "efivars"))
	if err != nil {
		return zero, fmt.Errorf("create efi bootloader: %w", err)
	}
	config := vz.NewVZVirtualMachineConfiguration()
	if config.ID == 0 {
		return zero, fmt.Errorf("create vm configuration")
	}
	config.SetBootLoader(&bootloader.VZBootLoader)
	config.SetCPUCount(1)
	config.SetMemorySize(2 << 30)
	return config, nil
}
