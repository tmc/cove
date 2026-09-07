//go:build darwin

package ios

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	privatevz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	networkx "github.com/tmc/apple/x/vzkit/network"
	"github.com/tmc/cove/internal/vmconfig"
)

type deviceGraph struct {
	configuration      vz.VZVirtualMachineConfiguration
	ecid               uint64
	serialInput        *os.File
	serialOutput       *os.File
	serialOutputWriter *os.File
	files              []*os.File
}

func (g *deviceGraph) close() {
	if g.configuration.ID != 0 {
		g.configuration.Release()
		g.configuration = vz.VZVirtualMachineConfiguration{}
	}
	for _, f := range g.files {
		f.Close()
	}
	g.files = nil
}

// buildGraph requires the main thread, an autorelease pool, and the bundle run
// lock. The caller owns the graph until after the VM is stopped and released.
func buildGraph(dir string, initialize bool, debugPort uint16) (_ *deviceGraph, err error) {
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		return nil, err
	}
	if cfg.IOS == nil {
		return nil, fmt.Errorf("bundle is not an ios guest")
	}
	if cfg.CPU == 0 || cfg.MemoryGB == 0 || cfg.MemoryGB > ^uint64(0)/(1<<30) {
		return nil, fmt.Errorf("invalid ios cpu or memory")
	}
	if debugPort != 0 && debugPort < 6000 {
		return nil, fmt.Errorf("invalid ios debug port")
	}
	rom, err := romFile(dir, cfg.IOS.ROM)
	if err != nil {
		return nil, fmt.Errorf("ios ap rom: %w", err)
	}
	sepROM := ""
	if cfg.IOS.SEPROM != "" {
		sepROM, err = romFile(dir, cfg.IOS.SEPROM)
		if err != nil {
			return nil, fmt.Errorf("ios sep rom: %w", err)
		}
	}
	disk, err := graphFile(dir, "disk.img")
	if err != nil {
		return nil, err
	}
	sep, err := graphFile(dir, "sep.img")
	if err != nil {
		return nil, err
	}
	if err := validateHardware(cfg.CPU, cfg.MemoryGB); err != nil {
		return nil, err
	}
	if err := probeResearchDevices(); err != nil {
		return nil, err
	}
	if err := probeHardwareModel(); err != nil {
		return nil, err
	}
	present, err := identityPresent(dir)
	if err != nil {
		return nil, err
	}
	if cfg.IOS.MAC == "" && cfg.IOS.Network != "none" {
		if !initialize || present {
			return nil, fmt.Errorf("ios network mac is missing; configure it while stopped")
		}
		var address [6]byte
		if _, err := rand.Read(address[:]); err != nil {
			return nil, fmt.Errorf("generate ios mac: %w", err)
		}
		address[0] = address[0]&0xfe | 2
		cfg.IOS.MAC = net.HardwareAddr(address[:]).String()
		if err := vmconfig.Save(dir, cfg); err != nil {
			return nil, err
		}
	}
	var scope objectScope
	defer func() { scope.release() }()
	g := &deviceGraph{}
	defer func() {
		if err != nil {
			g.close()
		}
	}()
	g.configuration = vz.NewVZVirtualMachineConfiguration()
	if g.configuration.ID == 0 {
		return nil, fmt.Errorf("create ios configuration: nil object")
	}
	platform, ecid, err := openPlatform(dir, initialize)
	if err != nil {
		return nil, err
	}
	if err := scope.add("platform", platform); err != nil {
		return nil, err
	}
	g.ecid = ecid
	g.configuration.SetPlatform(platform)
	g.configuration.SetCPUCount(cfg.CPU)
	g.configuration.SetMemorySize(cfg.MemoryGB << 30)
	if !present {
		args := cfg.IOS.BootArgs
		if args == "" {
			args = "serial=3 debug=0x104c04"
		}
		data := foundation.NewDataWithBytesLength([]byte(args))
		if err := scope.add("boot arguments", data); err != nil {
			return nil, err
		}
		aux := privatevz.VZMacAuxiliaryStorageFromID(platform.AuxiliaryStorage().GetID())
		ok, err := aux.SetDataValueForNVRAMVariableNamedError(data, objectivec.Object{ID: objc.String("boot-args")})
		if err != nil {
			return nil, fmt.Errorf("set ios boot arguments: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("set ios boot arguments: rejected")
		}
	}
	loader := vz.NewVZMacOSBootLoader()
	if err := scope.add("boot loader", loader); err != nil {
		return nil, err
	}
	romURL := foundation.NewURLFileURLWithPath(rom)
	if err := scope.add("ap rom url", romURL); err != nil {
		return nil, err
	}
	if err := privatevz.VZMacOSBootLoaderFromID(loader.ID).SetROMURL(romURL); err != nil {
		return nil, fmt.Errorf("set ios ap rom: %w", err)
	}
	g.configuration.SetBootLoader(loader)
	display := vz.NewMacGraphicsDisplayConfigurationWithWidthInPixelsHeightInPixelsPixelsPerInch(cfg.IOS.Display.Width, cfg.IOS.Display.Height, cfg.IOS.Display.PPI)
	if err := scope.add("display", display); err != nil {
		return nil, err
	}
	graphics := vz.NewVZMacGraphicsDeviceConfiguration()
	if err := scope.add("graphics", graphics); err != nil {
		return nil, err
	}
	graphics.SetDisplays([]vz.VZMacGraphicsDisplayConfiguration{display})
	g.configuration.SetGraphicsDevices([]vz.VZGraphicsDeviceConfiguration{graphics.VZGraphicsDeviceConfiguration})
	if err := configureAudio(g.configuration); err != nil {
		return nil, err
	}
	diskURL := foundation.NewURLFileURLWithPath(disk)
	if err := scope.add("disk url", diskURL); err != nil {
		return nil, err
	}
	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(diskURL, false)
	if err != nil {
		return nil, fmt.Errorf("attach ios disk: %w", err)
	}
	if err := scope.add("disk attachment", attachment); err != nil {
		return nil, err
	}
	block := vz.NewVirtioBlockDeviceConfigurationWithAttachment(attachment)
	if err := scope.add("block device", block); err != nil {
		return nil, err
	}
	g.configuration.SetStorageDevices([]vz.VZStorageDeviceConfiguration{block.VZStorageDeviceConfiguration})
	if cfg.IOS.Network != "none" {
		network, err := networkx.Parse(cfg.IOS.Network)
		if err != nil {
			return nil, err
		}
		attachment, err := networkx.CreateAttachment(network)
		if err != nil {
			return nil, fmt.Errorf("attach ios network: %w", err)
		}
		if err := scope.add("network attachment", attachment); err != nil {
			return nil, err
		}
		device := vz.NewVZVirtioNetworkDeviceConfiguration()
		if err := scope.add("network device", device); err != nil {
			return nil, err
		}
		mac := vz.NewMACAddressWithString(cfg.IOS.MAC)
		if err := scope.add("mac address", mac); err != nil {
			return nil, err
		}
		device.SetAttachment(attachment)
		device.SetMACAddress(mac)
		g.configuration.SetNetworkDevices([]vz.VZNetworkDeviceConfiguration{device.VZNetworkDeviceConfiguration})
	}
	entropy := vz.NewVZVirtioEntropyDeviceConfiguration()
	if err := scope.add("entropy", entropy); err != nil {
		return nil, err
	}
	g.configuration.SetEntropyDevices([]vz.VZEntropyDeviceConfiguration{entropy.VZEntropyDeviceConfiguration})
	keyboard := vz.NewVZUSBKeyboardConfiguration()
	if err := scope.add("keyboard", keyboard); err != nil {
		return nil, err
	}
	g.configuration.SetKeyboards([]vz.VZKeyboardConfiguration{keyboard.VZKeyboardConfiguration})
	if !cfg.IOS.NoVphoned {
		socket := vz.NewVZVirtioSocketDeviceConfiguration()
		if err := scope.add("vsock", socket); err != nil {
			return nil, err
		}
		g.configuration.SetSocketDevices([]vz.VZSocketDeviceConfiguration{socket.VZSocketDeviceConfiguration})
	}
	if err := g.configureSerial(); err != nil {
		return nil, err
	}
	if err := configureResearchDevices(g.configuration, sep, sepROM, debugPort); err != nil {
		return nil, err
	}
	valid, err := g.configuration.ValidateWithError()
	if err != nil {
		return nil, fmt.Errorf("validate ios configuration: %w", err)
	}
	if !valid {
		return nil, fmt.Errorf("validate ios configuration: rejected")
	}
	return g, nil
}

func graphFile(dir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("missing file reference")
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect ios file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("ios file must be nonempty and regular: %s", path)
	}
	return path, nil
}

func (g *deviceGraph) configureSerial() error {
	if objc.GetClass("_VZPL011SerialPortConfiguration") == 0 {
		return fmt.Errorf("ios pl011 class unavailable")
	}
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create ios serial input: %w", err)
	}
	g.files = append(g.files, inputRead, inputWrite)
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create ios serial output: %w", err)
	}
	g.files = append(g.files, outputRead, outputWrite)
	g.serialInput, g.serialOutput = inputWrite, outputRead
	g.serialOutputWriter = outputWrite
	var scope objectScope
	defer func() { scope.release() }()
	input := foundation.NewFileHandleWithFileDescriptorCloseOnDealloc(int32(inputRead.Fd()), false)
	if err := scope.add("serial input handle", input); err != nil {
		return err
	}
	output := foundation.NewFileHandleWithFileDescriptorCloseOnDealloc(int32(outputWrite.Fd()), false)
	if err := scope.add("serial output handle", output); err != nil {
		return err
	}
	attachment := vz.NewFileHandleSerialPortAttachmentWithFileHandleForReadingFileHandleForWriting(input, output)
	if err := scope.add("serial attachment", attachment); err != nil {
		return err
	}
	serial := privatevz.NewVZPL011SerialPortConfiguration()
	if err := scope.add("pl011", serial); err != nil {
		return err
	}
	port := vz.VZSerialPortConfigurationFromID(serial.ID)
	port.SetAttachment(attachment)
	g.configuration.SetSerialPorts([]vz.VZSerialPortConfiguration{port})
	return nil
}

func configureAudio(config vz.VZVirtualMachineConfiguration) error {
	var scope objectScope
	defer func() { scope.release() }()
	sound := vz.NewVZVirtioSoundDeviceConfiguration()
	if err := scope.add("sound", sound); err != nil {
		return err
	}
	input := vz.NewVZVirtioSoundDeviceInputStreamConfiguration()
	if err := scope.add("audio input", input); err != nil {
		return err
	}
	source := vz.NewVZHostAudioInputStreamSource()
	if err := scope.add("audio source", source); err != nil {
		return err
	}
	input.SetSource(source)
	output := vz.NewVZVirtioSoundDeviceOutputStreamConfiguration()
	if err := scope.add("audio output", output); err != nil {
		return err
	}
	sink := vz.NewVZHostAudioOutputStreamSink()
	if err := scope.add("audio sink", sink); err != nil {
		return err
	}
	output.SetSink(sink)
	sound.SetStreams([]vz.VZVirtioSoundDeviceStreamConfiguration{input.VZVirtioSoundDeviceStreamConfiguration, output.VZVirtioSoundDeviceStreamConfiguration})
	config.SetAudioDevices([]vz.VZAudioDeviceConfiguration{sound.VZAudioDeviceConfiguration})
	return nil
}
