// Command winbootprobe boots a guest headless and captures the actual guest
// framebuffer pixels via Virtualization's own screenshot API (not a window
// capture, so it is immune to Metal-layer black-capture artifacts).
//
// It answers the linear-framebuffer (LFB) question directly: with -graphics
// linear it boots the Windows install media and reads back what the guest
// painted into the private LFB. -macos <bundle> boots a known-good macOS guest
// as a positive control for the readback path.
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
	"github.com/tmc/apple/x/vzkit/debugstub"
	"github.com/tmc/apple/x/vzkit/framebuffer"
	platformx "github.com/tmc/apple/x/vzkit/platform"
	storagex "github.com/tmc/apple/x/vzkit/storage"
	windowsconfig "github.com/tmc/apple/x/vzkit/windowsconfig"
	"github.com/tmc/cove/internal/filehandle"
)

var (
	nat      = flag.Bool("nat", false, "attach a public NAT network device")
	pmu      = flag.Bool("pmu", false, "enable private generic-platform PMU emulation")
	nbdURL   = flag.String("nbd", "", "use this NBD URL for the scratch EFI USB disk instead of its local file")
	pcapPath = flag.String("pcap", "", "attach isolated capture-only NIC and write Ethernet pcap (no DHCP replies)")
	gdbPort  = flag.Uint("gdb", 0, "private guest GDB port on loopback (requires restricted entitlement)")
	efiImage = flag.String("efi", "", "scratch EFI disk to boot read-write without a Windows disk")
	vmdir    = flag.String("vmdir", os.ExpandEnv("$HOME/.vz/vms/windows-swiftui.covevm"), "Windows VM bundle directory")
	macosDir = flag.String("macos", "", "boot a macOS guest from this state dir instead (positive control)")
	graphics = flag.String("graphics", "linear", "graphics device: linear or virtio (Windows only)")
	width    = flag.Int("width", 1920, "framebuffer width")
	height   = flag.Int("height", 1200, "framebuffer height")
	seconds  = flag.Int("seconds", 40, "observation window")
	shot     = flag.String("shot", "shot", "PNG basename; frames saved as <shot>-<t>s.png")
	bootOnly = flag.Bool("boot", false, "boot-only: no delegate, no capture (isolate crashes)")
	noCap    = flag.Bool("nocap", false, "keep delegate but skip capture (isolate capture vs delegate)")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nRESULT: FAILED — %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if *nat && *pcapPath != "" {
		return fmt.Errorf("nat and pcap are mutually exclusive")
	}
	if *pmu && *macosDir != "" {
		return fmt.Errorf("pmu requires a generic EFI guest")
	}
	if *gdbPort > 65535 {
		return fmt.Errorf("gdb port exceeds 65535")
	}
	if *seconds <= 0 {
		return fmt.Errorf("seconds must be positive")
	}
	if *nbdURL != "" && *efiImage == "" {
		return fmt.Errorf("nbd requires a scratch efi image")
	}
	if *efiImage != "" && *macosDir != "" {
		return fmt.Errorf("efi and macos are mutually exclusive")
	}
	config, err := buildConfig()
	if err != nil {
		return err
	}
	if *nat {
		attachment := vz.NewVZNATNetworkDeviceAttachment()
		device := vz.NewVZVirtioNetworkDeviceConfiguration()
		if attachment.ID == 0 || device.ID == 0 {
			return fmt.Errorf("create NAT network device")
		}
		device.SetAttachment(&attachment.VZNetworkDeviceAttachment)
		mac := vz.GetVZMACAddressClass().RandomLocallyAdministeredAddress()
		device.SetMACAddress(&mac)
		config.SetNetworkDevices([]vz.VZNetworkDeviceConfiguration{device.VZNetworkDeviceConfiguration})
		fmt.Printf("NAT network: %s\n", mac.String())
	}
	if *pcapPath != "" {
		session, err := filehandle.NewSession(filehandle.Config{PCAPPath: *pcapPath})
		if err != nil {
			return fmt.Errorf("create capture NIC: %w", err)
		}
		device := session.DeviceConfiguration()
		config.SetNetworkDevices([]vz.VZNetworkDeviceConfiguration{device.VZNetworkDeviceConfiguration})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- session.Pump(ctx, nil) }()
		defer func() {
			cancel()
			<-done
			fmt.Println(session.Summary())
			session.Close()
		}()
	}
	if *gdbPort != 0 {
		if err := debugstub.AttachGDB(pvz.VZVirtualMachineConfigurationFromID(config.ID), uint16(*gdbPort), false); err != nil {
			return fmt.Errorf("attach guest debugger: %w", err)
		}
	}
	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	config.Retain()
	fmt.Println("config valid")

	queue := dispatch.QueueCreate("com.tmc.cove.winbootprobe")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, queue)
	if vm.ID == 0 {
		return fmt.Errorf("create VM")
	}
	vm.Retain()

	// Surface the real reason a guest stops or errors.
	if !*bootOnly {
		queue.Async(func() {
			del := vz.NewVZVirtualMachineDelegate(vz.VZVirtualMachineDelegateConfig{
				VirtualMachineDidStopWithError: func(_ vz.VZVirtualMachine, e foundation.NSError) {
					fmt.Printf("  DELEGATE didStopWithError: %s\n", e.LocalizedDescription())
				},
				GuestDidStopVirtualMachine: func(_ vz.VZVirtualMachine) {
					fmt.Println("  DELEGATE guestDidStop (clean shutdown)")
				},
			})
			vm.SetDelegate(del)
		})
	}

	start := time.Now()
	errc := make(chan error, 1)
	queue.Async(func() { vm.StartWithCompletionHandler(func(err error) { errc <- err }) })
	select {
	case err := <-errc:
		if err != nil {
			if e, ok := err.(*foundation.NSError); ok {
				fmt.Fprintf(os.Stderr, "start NSError: domain=%s code=%d userInfo=%s\n", e.Domain(), e.Code(), e.UserInfo().Description())
			}
			return fmt.Errorf("start refused: %w", err)
		}
	case <-time.After(20 * time.Second):
		return fmt.Errorf("start timed out")
	}
	fmt.Printf("[%6.2fs] started; state=%s\n", time.Since(start).Seconds(), currentState(queue, vm))

	// Capture frames as boot progresses.
	shots := []int{3, 6, 9, 13, 18, 25, 35}
	for len(shots) > 0 && shots[len(shots)-1] >= *seconds {
		shots = shots[:len(shots)-1]
	}
	shots = append(shots, *seconds)
	for _, t := range shots {
		for time.Since(start) < time.Duration(t)*time.Second {
			time.Sleep(250 * time.Millisecond)
		}
		st := currentState(queue, vm)
		if st == vz.VZVirtualMachineStateStopped {
			fmt.Println("guest stopped")
			return nil
		}
		if st == vz.VZVirtualMachineStateError {
			return fmt.Errorf("guest entered error state")
		}
		path := fmt.Sprintf("%s-%02ds.png", *shot, t)
		if *bootOnly || *noCap {
			fmt.Printf("[%6.2fs] state=%s (no-capture)\n", time.Since(start).Seconds(), st)
			continue
		}
		if err := capture(queue, vm, path); err != nil {
			fmt.Printf("[%6.2fs] state=%s  capture: %v\n", time.Since(start).Seconds(), st, err)
			continue
		}
		nb, mn, mx := pngStats(path)
		fmt.Printf("[%6.2fs] state=%s  saved %s  (%d bytes, pixel min=%d max=%d)\n",
			time.Since(start).Seconds(), st, filepath.Base(path), nb, mn, mx)
	}

	// stop
	done := make(chan error, 1)
	queue.Async(func() {
		vm.StopWithCompletionHandler(func(err error) { done <- err })
	})
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("stop: %w", err)
		}
	case <-time.After(3 * time.Second):
		return fmt.Errorf("stop timed out")
	}
	return nil
}

func capture(queue dispatch.Queue, vm vz.VZVirtualMachine, path string) error {
	idch := make(chan objc.ID, 1)
	queue.Async(func() {
		defer func() {
			if recover() != nil {
				idch <- 0
			}
		}()
		devs := vm.GraphicsDevices()
		if len(devs) == 0 {
			idch <- 0
			return
		}
		disps := devs[0].Displays()
		if len(disps) == 0 {
			idch <- 0
			return
		}
		idch <- disps[0].Object.ID
	})
	var id objc.ID
	select {
	case id = <-idch:
	case <-time.After(3 * time.Second):
		return fmt.Errorf("no display (timeout)")
	}
	if id == 0 {
		return fmt.Errorf("no graphics display on running VM")
	}
	d := framebuffer.FromGraphicsDisplayID(objectivec.ObjectFromID(id))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return d.CapturePNG(ctx, path)
}

func currentState(queue dispatch.Queue, vm vz.VZVirtualMachine) vz.VZVirtualMachineState {
	ch := make(chan vz.VZVirtualMachineState, 1)
	queue.Async(func() { ch <- vm.State() })
	select {
	case s := <-ch:
		return s
	case <-time.After(2 * time.Second):
		return vz.VZVirtualMachineState(-1)
	}
}

func buildConfig() (vz.VZVirtualMachineConfiguration, error) {
	if *macosDir != "" {
		fmt.Println("guest: macOS (positive control)")
		return vzkit.BuildMacVMConfig(vzkit.MacVMConfig{
			CPUs: 4, MemoryGB: 8,
			DiskPath: filepath.Join(*macosDir, "disk.img"),
			StateDir: *macosDir,
			Network:  vzkit.NetworkConfig{Mode: vzkit.NetworkModeNAT},
		})
	}

	bootImg := filepath.Join(*vmdir, "efi-boot.img")
	diskImg := filepath.Join(*vmdir, "windows-disk.img")
	paths := []string{bootImg, diskImg}
	if *efiImage != "" {
		bootImg = *efiImage
		paths = []string{bootImg}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("missing %s", p)
		}
	}
	scratch, err := os.MkdirTemp("", "winbootprobe")
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, err
	}
	nvram := filepath.Join(scratch, "efi.nvram")
	if *efiImage == "" {
		if err := copyFile(filepath.Join(*vmdir, "efi.nvram"), nvram); err != nil {
			return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("copy EFI store: %w", err)
		}
	}
	fmt.Printf("EFI state: %s\n", scratch)

	config, err := windowsconfig.Build(windowsconfig.Config{
		CPUCount: 4, MemoryGB: 8,
		Keyboard: true, Pointing: true, Entropy: true, USBController: true, MemoryBalloon: true,
	})
	if err != nil {
		return config, err
	}
	platform := vz.NewVZGenericPlatformConfiguration()
	if *pmu {
		privatePlatform := pvz.VZGenericPlatformConfigurationFromID(platform.ID)
		if err := privatePlatform.SetPerformanceMonitoringUnitEmulationEnabled(true); err != nil {
			return config, fmt.Errorf("enable PMU emulation: %w", err)
		}
		enabled, err := privatePlatform.PerformanceMonitoringUnitEmulationEnabled()
		if err != nil {
			return config, fmt.Errorf("read PMU emulation setting: %w", err)
		}
		if !enabled {
			return config, fmt.Errorf("PMU emulation setting was not retained")
		}
		fmt.Println("generic platform: PMU emulation enabled")
	}
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
		fmt.Printf("guest: Windows, graphics=linear framebuffer %dx%d\n", *width, *height)
	case "virtio":
		scanout := vz.NewVirtioGraphicsScanoutConfigurationWithWidthInPixelsHeightInPixels(*width, *height)
		gfx := vz.NewVZVirtioGraphicsDeviceConfiguration()
		gfx.SetScanouts([]vz.VZVirtioGraphicsScanoutConfiguration{scanout})
		config.SetGraphicsDevices([]vz.VZGraphicsDeviceConfiguration{vz.VZGraphicsDeviceConfigurationFromID(gfx.ID)})
		fmt.Printf("guest: Windows, graphics=virtio %dx%d\n", *width, *height)
	default:
		return config, fmt.Errorf("unknown -graphics %q", *graphics)
	}

	bootDev, err := usbStorage(bootImg, *efiImage == "")
	if err != nil {
		return config, err
	}
	if *efiImage != "" {
		config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{bootDev})
		return config, nil
	}
	diskDev, err := nvmeStorage(diskImg, true)
	if err != nil {
		return config, err
	}
	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{diskDev, bootDev})
	return config, nil
}

func usbStorage(path string, ro bool) (vz.VZStorageDeviceConfiguration, error) {
	if *nbdURL != "" {
		url := foundation.NewURLWithString(*nbdURL)
		if url.ID == 0 {
			return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("invalid NBD URL")
		}
		att, err := vz.NewNetworkBlockDeviceStorageDeviceAttachmentWithURLError(url)
		if err != nil {
			return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("attach NBD disk: %w", err)
		}
		dev, err := storagex.CreateUSBMassStorageDeviceWithAttachment(att.VZStorageDeviceAttachment)
		if err != nil {
			return vz.VZStorageDeviceConfiguration{}, err
		}
		return vz.VZStorageDeviceConfigurationFromID(dev.ID), nil
	}
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

// pngStats returns the file size and the min/max luminance across all pixels,
// so a black frame (min==max==0) is distinguishable from a painted one.
func pngStats(path string) (bytes int, min, max int) {
	fi, err := os.Stat(path)
	if err == nil {
		bytes = int(fi.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return bytes, 0, 0
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return bytes, 0, 0
	}
	min = 1 << 30
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := img.At(x, y).RGBA()
			v := int((r + g + bl) / 3 >> 8)
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	if min == 1<<30 {
		min = 0
	}
	return bytes, min, max
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0644)
}
