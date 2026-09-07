// Command winbootgui boots the Windows ARM64 install image with the private
// linear framebuffer graphics device and shows it in a real AppKit window,
// backed by the public VZVirtualMachineView. If Windows Setup paints through
// the LFB's GOP, its WinPE UI appears in the window.
//
// It is the visual companion to winbootprobe: same non-destructive config
// (scratch EFI store, read-only disk), but headed instead of headless.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
	"github.com/tmc/apple/x/vzkit/framebuffer"
	platformx "github.com/tmc/apple/x/vzkit/platform"
	storagex "github.com/tmc/apple/x/vzkit/storage"
	windowsconfig "github.com/tmc/apple/x/vzkit/windowsconfig"
)

func init() { runtime.LockOSThread() }

var (
	vmdir    = flag.String("vmdir", os.ExpandEnv("$HOME/.vz/vms/windows-swiftui.covevm"), "VM bundle directory")
	graphics = flag.String("graphics", "linear", "graphics device: linear or virtio")
	width    = flag.Int("width", 1920, "framebuffer width")
	height   = flag.Int("height", 1200, "framebuffer height")
	macosDir = flag.String("macos", "", "boot a macOS guest from this bundle dir instead (control: proves the view path paints)")
)

func main() {
	flag.Parse()

	config, err := buildConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if _, err := config.ValidateWithError(); err != nil {
		fmt.Fprintf(os.Stderr, "validation failed: %v\n", err)
		os.Exit(1)
	}
	config.Retain()
	fmt.Println("config valid")

	queue := dispatch.QueueCreate("com.tmc.cove.winbootgui")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, queue)
	if vm.ID == 0 {
		fmt.Fprintln(os.Stderr, "create VM failed")
		os.Exit(1)
	}
	vm.Retain()

	appkit.RunApp(func(app appkit.NSApplication, _ appkit.NSApplicationDelegateObject) {
		contentRect := corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 100, Y: 100},
			Size:   corefoundation.CGSize{Width: 1280, Height: 800},
		}
		window := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			contentRect,
			appkit.NSWindowStyleMaskTitled|appkit.NSWindowStyleMaskClosable|
				appkit.NSWindowStyleMaskMiniaturizable|appkit.NSWindowStyleMaskResizable,
			appkit.NSBackingStoreBuffered,
			false,
		)
		window.SetTitle(fmt.Sprintf("Windows on LFB (%s)", *graphics))
		window.SetReleasedWhenClosed(false)

		view := vz.NewVZVirtualMachineView()
		view.SetVirtualMachine(&vm)
		window.SetContentView(appkit.NSViewFromID(view.ID))
		window.Center()

		delegate := appkit.NewNSWindowDelegate(appkit.NSWindowDelegateConfig{
			ShouldClose: func(_ appkit.NSWindow) bool {
				queue.Async(func() {
					defer func() { recover() }()
					vm.StopWithCompletionHandler(func(error) {})
				})
				go func() { time.Sleep(500 * time.Millisecond); app.Terminate(nil) }()
				return true
			},
		})
		window.SetDelegate(delegate)

		window.MakeKeyAndOrderFront(nil)
		app.Activate()

		fmt.Println("window open — starting VM; close the window to quit")
		start := time.Now()
		queue.Async(func() {
			vm.StartWithCompletionHandler(func(err error) {
				if err != nil {
					fmt.Printf("[%6.2fs] start refused: %v\n", time.Since(start).Seconds(), err)
					return
				}
				fmt.Printf("[%6.2fs] started; state=%s\n", time.Since(start).Seconds(), vm.State())
			})
		})
	})
}

func buildConfig() (vz.VZVirtualMachineConfiguration, error) {
	if *macosDir != "" {
		return vzkit.BuildMacVMConfig(vzkit.MacVMConfig{
			CPUs:     4,
			MemoryGB: 8,
			DiskPath: filepath.Join(*macosDir, "disk.img"),
			StateDir: *macosDir,
			Network:  vzkit.NetworkConfig{Mode: vzkit.NetworkModeNAT},
		})
	}
	bootImg := filepath.Join(*vmdir, "efi-boot.img")
	diskImg := filepath.Join(*vmdir, "windows-disk.img")
	for _, p := range []string{bootImg, diskImg} {
		if _, err := os.Stat(p); err != nil {
			return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("missing %s", p)
		}
	}
	scratch, err := os.MkdirTemp("", "winbootgui")
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, err
	}
	nvram := filepath.Join(scratch, "efi.nvram")
	_ = copyFile(filepath.Join(*vmdir, "efi.nvram"), nvram)

	config, err := windowsconfig.Build(windowsconfig.Config{
		CPUCount: 4, MemoryGB: 8,
		Keyboard: true, Pointing: true, Entropy: true, USBController: true, MemoryBalloon: true,
	})
	if err != nil {
		return config, err
	}

	platform := vz.NewVZGenericPlatformConfiguration()
	mid, _, err := platformx.LoadOrCreateGenericMachineIdentifier(filepath.Join(scratch, "machine.id"))
	if err != nil {
		return config, err
	}
	platform.SetMachineIdentifier(&mid)
	config.SetPlatform(&platform.VZPlatformConfiguration)

	bootloader, _, err := platformx.CreateEFIBootLoader(nvram)
	if err != nil {
		return config, err
	}
	config.SetBootLoader(&bootloader.VZBootLoader)

	switch *graphics {
	case "linear":
		if err := framebuffer.SetLinearFramebufferGraphicsDevice(config, framebuffer.LinearFramebufferConfig{Width: *width, Height: *height}); err != nil {
			return config, err
		}
	case "virtio":
		scanout := vz.NewVirtioGraphicsScanoutConfigurationWithWidthInPixelsHeightInPixels(*width, *height)
		gfx := vz.NewVZVirtioGraphicsDeviceConfiguration()
		gfx.SetScanouts([]vz.VZVirtioGraphicsScanoutConfiguration{scanout})
		config.SetGraphicsDevices([]vz.VZGraphicsDeviceConfiguration{vz.VZGraphicsDeviceConfigurationFromID(gfx.ID)})
	default:
		return config, fmt.Errorf("unknown -graphics %q", *graphics)
	}

	bootDev, err := usbStorage(bootImg, true)
	if err != nil {
		return config, err
	}
	diskDev, err := nvmeStorage(diskImg, true)
	if err != nil {
		return config, err
	}
	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{diskDev, bootDev})
	return config, nil
}

func usbStorage(path string, ro bool) (vz.VZStorageDeviceConfiguration, error) {
	att, err := attach(path, ro)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	dev, err := storagex.CreateUSBMassStorageDeviceWithAttachment(att.VZStorageDeviceAttachment)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	return vz.VZStorageDeviceConfigurationFromID(dev.ID), nil
}

func nvmeStorage(path string, ro bool) (vz.VZStorageDeviceConfiguration, error) {
	att, err := attach(path, ro)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	dev, err := storagex.CreateNVMeDeviceWithAttachment(att.VZStorageDeviceAttachment)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	return vz.VZStorageDeviceConfigurationFromID(dev.ID), nil
}

func attach(path string, ro bool) (vz.VZDiskImageStorageDeviceAttachment, error) {
	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return vz.VZDiskImageStorageDeviceAttachment{}, fmt.Errorf("file url for %s", path)
	}
	url.Retain()
	policy := storagex.CacheDurable
	if ro {
		policy = storagex.CacheReadOnly
	}
	att, err := storagex.NewDiskImageAttachment(url, ro, policy)
	if err != nil {
		return vz.VZDiskImageStorageDeviceAttachment{}, err
	}
	att.Retain()
	return att, nil
}

var _ = objectivec.ObjectFromID

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0644)
}
