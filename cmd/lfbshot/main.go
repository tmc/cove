// Command lfbshot boots a Windows ARM64 install image with the private linear
// framebuffer (LFB) graphics device and captures the pixels the guest paints
// into it, writing them out as PNGs.
//
// It reuses winbootprobe's non-destructive boot recipe (scratch nvram, read-only
// media) and adds real pixel capture: it drives
// -[VZGraphicsDisplay _takeScreenshotWithCompletionHandler:], whose true
// completion ABI is void(^)(CGImageRef, NSError*), and converts the returned
// CGImage to a PNG. It captures at t~=5s (before WinPE paints) and t~=22s (after
// Setup should have painted) so the two frames can be diffed.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/framebuffer"
	platformx "github.com/tmc/apple/x/vzkit/platform"
	storagex "github.com/tmc/apple/x/vzkit/storage"
	windowsconfig "github.com/tmc/apple/x/vzkit/windowsconfig"
)

func main() {
	vmdir := flag.String("vmdir", os.ExpandEnv("$HOME/.vz/vms/windows-swiftui.covevm"), "VM bundle directory")
	width := flag.Int("width", 1920, "framebuffer width")
	height := flag.Int("height", 1200, "framebuffer height")
	outdir := flag.String("outdir", ".", "directory for PNG output")
	flag.Parse()

	if err := run(*vmdir, *width, *height, *outdir); err != nil {
		fmt.Fprintf(os.Stderr, "\nRESULT: FAILED — %v\n", err)
		os.Exit(1)
	}
}

func run(vmdir string, width, height int, outdir string) error {
	bootImg := filepath.Join(vmdir, "efi-boot.img")
	diskImg := filepath.Join(vmdir, "windows-disk.img")
	for _, p := range []string{bootImg, diskImg} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("bundle file missing: %s", p)
		}
	}

	scratch, err := os.MkdirTemp("", "lfbshot")
	if err != nil {
		return err
	}
	nvram := filepath.Join(scratch, "efi.nvram")
	if err := copyFile(filepath.Join(vmdir, "efi.nvram"), nvram); err != nil {
		nvram = filepath.Join(scratch, "efi.nvram")
	}

	config, err := windowsconfig.Build(windowsconfig.Config{
		CPUCount:      4,
		MemoryGB:      8,
		Keyboard:      true,
		Pointing:      true,
		Entropy:       true,
		USBController: true,
		MemoryBalloon: true,
	})
	if err != nil {
		return fmt.Errorf("build device config: %w", err)
	}

	platform := vz.NewVZGenericPlatformConfiguration()
	mid, _, err := platformx.LoadOrCreateGenericMachineIdentifier(filepath.Join(scratch, "machine.id"))
	if err != nil {
		return fmt.Errorf("machine id: %w", err)
	}
	platform.SetMachineIdentifier(&mid)
	config.SetPlatform(&platform.VZPlatformConfiguration)

	bootloader, _, err := platformx.CreateEFIBootLoader(nvram)
	if err != nil {
		return fmt.Errorf("efi bootloader: %w", err)
	}
	config.SetBootLoader(&bootloader.VZBootLoader)

	if err := framebuffer.SetLinearFramebufferGraphicsDevice(config, framebuffer.LinearFramebufferConfig{Width: width, Height: height}); err != nil {
		return fmt.Errorf("set linear framebuffer: %w", err)
	}
	fmt.Printf("graphics: linear framebuffer %dx%d\n", width, height)

	bootDev, err := usbStorage(bootImg, true)
	if err != nil {
		return fmt.Errorf("boot image: %w", err)
	}
	diskDev, err := nvmeStorage(diskImg, true)
	if err != nil {
		return fmt.Errorf("disk image: %w", err)
	}
	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{diskDev, bootDev})

	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	config.Retain()
	fmt.Println("config valid")

	queue := dispatch.QueueCreate("com.tmc.cove.lfbshot")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, queue)
	if vm.ID == 0 {
		return fmt.Errorf("create VM")
	}
	vm.Retain()

	start := time.Now()
	errc := make(chan error, 1)
	queue.Async(func() { vm.StartWithCompletionHandler(func(err error) { errc <- err }) })
	select {
	case err := <-errc:
		if err != nil {
			return fmt.Errorf("start refused: %w", err)
		}
	case <-time.After(20 * time.Second):
		return fmt.Errorf("start timed out")
	}
	fmt.Printf("[%6.2fs] started\n", time.Since(start).Seconds())

	// Capture pre-paint (~5s) and post-paint (~22s).
	var pre, post image.Image
	captures := []struct {
		at   time.Duration
		name string
		dst  *image.Image
	}{
		{5 * time.Second, "lfbshot-pre.png", &pre},
		{22 * time.Second, "lfbshot-post.png", &post},
	}
	var paths []string
	for _, c := range captures {
		if d := c.at - time.Since(start); d > 0 {
			time.Sleep(d)
		}
		st := currentState(queue, vm)
		fmt.Printf("[%6.2fs] state %s, capturing %s\n", time.Since(start).Seconds(), st, c.name)
		img, err := capture(queue, vm)
		if err != nil {
			fmt.Printf("        capture error: %v\n", err)
			continue
		}
		*c.dst = img
		p := filepath.Join(outdir, c.name)
		if err := writePNG(p, img); err != nil {
			fmt.Printf("        write error: %v\n", err)
			continue
		}
		b := img.Bounds()
		fmt.Printf("        wrote %s (%dx%d)\n", p, b.Dx(), b.Dy())
		paths = append(paths, p)
	}

	// Also write the "main" screenshot name pointing at the post-paint frame.
	if post != nil {
		p := filepath.Join(outdir, "lfbshot.png")
		if err := writePNG(p, post); err == nil {
			fmt.Printf("        wrote %s\n", p)
			paths = append(paths, p)
		}
	}

	fmt.Printf("[%6.2fs] final state: %s\n", time.Since(start).Seconds(), currentState(queue, vm))
	if pre != nil && post != nil {
		fmt.Printf("diff pre/post: %s\n", diffSummary(pre, post))
	}
	fmt.Println("\nPNG paths:")
	for _, p := range paths {
		fmt.Println("  " + p)
	}

	// Best-effort stop.
	done := make(chan struct{}, 1)
	queue.Async(func() {
		defer func() { recover(); done <- struct{}{} }()
		vm.StopWithCompletionHandler(func(error) {})
	})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	return nil
}

// capture grabs one screenshot of the first display of the first graphics
// device on the running VM. All Virtualization calls run on the VM's queue.
func capture(queue dispatch.Queue, vm vz.VZVirtualMachine) (image.Image, error) {
	type res struct {
		img image.Image
		err error
	}
	ch := make(chan res, 1)
	queue.Async(func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- res{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		devs := vm.GraphicsDevices()
		if len(devs) == 0 {
			ch <- res{err: fmt.Errorf("no graphics devices")}
			return
		}
		disps := devs[0].Displays()
		if len(disps) == 0 {
			ch <- res{err: fmt.Errorf("graphics device has 0 displays")}
			return
		}
		d := framebuffer.FromGraphicsDisplayID(objectivec.IObject(disps[0].Object))
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		img, err := d.CaptureImage(ctx)
		ch <- res{img: img, err: err}
	})
	select {
	case r := <-ch:
		return r.img, r.err
	case <-time.After(12 * time.Second):
		return nil, fmt.Errorf("capture timed out")
	}
}

// diffSummary reports how many pixels differ between two images and the mean
// absolute per-channel difference, so a blank vs painted frame is obvious.
func diffSummary(a, b image.Image) string {
	ra, aok := a.(*image.RGBA)
	rb, bok := b.(*image.RGBA)
	if !aok || !bok || len(ra.Pix) != len(rb.Pix) || len(ra.Pix) == 0 {
		return "incomparable"
	}
	var diffPixels int64
	var sum int64
	for i := 0; i < len(ra.Pix); i += 4 {
		if ra.Pix[i] != rb.Pix[i] || ra.Pix[i+1] != rb.Pix[i+1] || ra.Pix[i+2] != rb.Pix[i+2] {
			diffPixels++
		}
		for c := 0; c < 3; c++ {
			d := int64(ra.Pix[i+c]) - int64(rb.Pix[i+c])
			if d < 0 {
				d = -d
			}
			sum += d
		}
	}
	total := int64(len(ra.Pix) / 4)
	mean := float64(sum) / float64(len(ra.Pix)/4*3)
	return fmt.Sprintf("%d/%d pixels differ (%.1f%%), mean abs channel diff %.2f",
		diffPixels, total, 100*float64(diffPixels)/float64(total), mean)
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return f.Close()
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

func usbStorage(path string, readOnly bool) (vz.VZStorageDeviceConfiguration, error) {
	att, err := attach(path, readOnly)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	dev, err := storagex.CreateUSBMassStorageDeviceWithAttachment(att.VZStorageDeviceAttachment)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	return vz.VZStorageDeviceConfigurationFromID(dev.ID), nil
}

func nvmeStorage(path string, readOnly bool) (vz.VZStorageDeviceConfiguration, error) {
	att, err := attach(path, readOnly)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	dev, err := storagex.CreateNVMeDeviceWithAttachment(att.VZStorageDeviceAttachment)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	return vz.VZStorageDeviceConfigurationFromID(dev.ID), nil
}

func attach(path string, readOnly bool) (vz.VZDiskImageStorageDeviceAttachment, error) {
	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return vz.VZDiskImageStorageDeviceAttachment{}, fmt.Errorf("file url for %s", path)
	}
	url.Retain()
	policy := storagex.CacheDurable
	if readOnly {
		policy = storagex.CacheReadOnly
	}
	att, err := storagex.NewDiskImageAttachment(url, readOnly, policy)
	if err != nil {
		return vz.VZDiskImageStorageDeviceAttachment{}, err
	}
	att.Retain()
	return att, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
