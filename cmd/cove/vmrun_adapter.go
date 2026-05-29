package main

import (
	"time"

	vz "github.com/tmc/apple/virtualization"
	displayx "github.com/tmc/apple/x/vzkit/display"
	"github.com/tmc/cove/internal/vmrun"
)

type runtimeOptions struct {
	VMName  string
	VMDir   string
	Verbose bool
	Fleet   string

	Linux   bool
	Windows bool

	CPUCount   uint
	MemoryGB   uint64
	DiskPath   string
	DiskSizeGB uint64
	RawDisk    bool

	IPSWPath   string
	ISOPath    string
	KernelPath string
	InitrdPath string
	CmdLine    string
	BootArgs   string

	GUI          bool
	Headless     bool
	RecoveryMode bool
	ForceDFU     bool
	StopIBoot1   bool
	StopIBoot2   bool
	ForceInstall bool
	SkipResume   bool
	Unattended   bool
	InstallVM    bool

	NetworkMode     string
	HTTPListenAddr  string
	SandboxLevel    string
	HostContainment bool

	EnableRosetta   bool
	EnableClipboard bool
	SaveCompress    bool
	SaveEncrypt     bool
	GDBAddress      string
	GDBListenAll    bool

	LinuxNested bool
	LinuxNVMe   bool
	LinuxShell  bool
	CPUExplicit bool

	WindowsGraphicsMode string
	WindowsSerialMode   string
	WindowsEFIRomPath   string

	ProvisionUser     string
	ProvisionPassword string
	ProvisionAdmin    bool
	ProvisionStrategy string

	SerialOutput     string
	StartTimeout     time.Duration
	BootCommandsFile string

	AutomationBackend        string
	AutomationCaptureBackend string
	AutomationInputBackend   string
	DiskSyncMode             string
	VNCAddress               string
	VNCPassword              string
	VNCBonjourService        string

	RuntimeProfile       string
	LaunchOrder          string
	AutoMountVolumes     bool
	AutoUpgradeAgent     bool
	RecoverIdentity      bool
	PreferPasswordDialog bool

	Disposable               bool
	RollbackSnapshot         string
	DisposableSourceDiskPath string
	SystemDiskAttachment     systemDiskAttachmentMode
	SystemDiskPathOverride   string
	EphemeralForkParent      string
	EphemeralForkName        string
	EphemeralForkKeep        bool
	Ephemeral                bool

	Displays            displayx.Slice
	USBDevices          USBStorageSlice
	BlockDevices        blockDeviceSlice
	Volumes             volumeSlice
	StartupPortForwards portForwardSpecs
}

func currentRuntimeOptions() runtimeOptions {
	return runtimeOptions{
		VMName:  vmName,
		VMDir:   vmDir,
		Verbose: verbose,
		Fleet:   fleetName,

		Linux:   linuxMode,
		Windows: windowsMode,

		CPUCount:   cpuCount,
		MemoryGB:   memoryGB,
		DiskPath:   diskPath,
		DiskSizeGB: diskSizeGB,
		RawDisk:    rawDisk,

		IPSWPath:   ipswPath,
		ISOPath:    isoPath,
		KernelPath: kernelPath,
		InitrdPath: initrdPath,
		CmdLine:    cmdLine,
		BootArgs:   bootArgs,

		GUI:          guiMode,
		Headless:     headlessMode,
		RecoveryMode: recoveryMode,
		ForceDFU:     forceDFU,
		StopIBoot1:   stopInIBootStage1,
		StopIBoot2:   stopInIBootStage2,
		ForceInstall: forceInstall,
		SkipResume:   skipResume,
		Unattended:   unattended,
		InstallVM:    installVM,

		NetworkMode:     networkMode,
		HTTPListenAddr:  runHTTPAddr,
		SandboxLevel:    effectiveSandboxMode(),
		HostContainment: hostContainment,

		EnableRosetta:   enableRosetta,
		EnableClipboard: enableClipboard,
		SaveCompress:    saveCompress,
		SaveEncrypt:     saveEncrypt,
		GDBAddress:      gdbAddress,
		GDBListenAll:    gdbListenAll,

		LinuxNested: linuxNested,
		LinuxNVMe:   linuxNVMe,
		LinuxShell:  linuxShell,
		CPUExplicit: cpuExplicit,

		WindowsGraphicsMode: windowsGraphicsMode,
		WindowsSerialMode:   windowsSerialMode,
		WindowsEFIRomPath:   windowsEFIRomPath,

		ProvisionUser:     provisionUser,
		ProvisionPassword: provisionPassword,
		ProvisionAdmin:    provisionAdmin,
		ProvisionStrategy: provisionStrategy,

		SerialOutput:     serialOutput,
		StartTimeout:     startTimeout,
		BootCommandsFile: bootCommandsFile,

		AutomationBackend:        automationBackend,
		AutomationCaptureBackend: automationCaptureBackend,
		AutomationInputBackend:   automationInputBackend,
		DiskSyncMode:             diskSyncMode,
		VNCAddress:               vncAddress,
		VNCPassword:              vncPassword,
		VNCBonjourService:        vncBonjourService,

		RuntimeProfile:       runtimeProfile,
		LaunchOrder:          launchOrder,
		AutoMountVolumes:     autoMountVolumes,
		AutoUpgradeAgent:     autoUpgradeAgent,
		RecoverIdentity:      recoverIdentity,
		PreferPasswordDialog: preferPasswordDialog,

		Disposable:               disposableMode,
		RollbackSnapshot:         rollbackSnapshotName,
		DisposableSourceDiskPath: disposableSourceDiskPath,
		SystemDiskAttachment:     runtimeSystemDiskAttachment,
		SystemDiskPathOverride:   runtimeSystemDiskPathOverride,
		EphemeralForkParent:      ephemeralForkParent,
		EphemeralForkName:        ephemeralForkName,
		EphemeralForkKeep:        ephemeralForkKeep,
		Ephemeral:                runEphemeral,

		Displays:            displays,
		USBDevices:          usbDevices,
		BlockDevices:        blockDevices,
		Volumes:             volumes,
		StartupPortForwards: startupPortForwards,
	}
}

func (opts runtimeOptions) vmSelection() vmSelection {
	return vmSelection{
		Directory: opts.VMDir,
		Name:      opts.VMName,
	}
}

// vmrunRunConfig snapshots the relevant package-level globals into a
// vmrun.RunConfig for the given guest OS. Slice 5 of design 039 introduces
// this boundary so entry points (macos.go, linux.go, windows.go) read run
// state from a value rather than directly from globals.
//
// Slice 5 keeps flag parsing in main.go untouched; later slices retire the
// globals once a thicker command environment owns the values.
func vmrunRunConfig(os vmrun.GuestOS) vmrun.RunConfig {
	return currentRuntimeOptions().vmrunRunConfig(os)
}

func (opts runtimeOptions) vmrunRunConfig(os vmrun.GuestOS) vmrun.RunConfig {
	rc := vmrun.RunConfig{
		OS:                  os,
		CPUCount:            opts.CPUCount,
		MemoryGB:            opts.MemoryGB,
		DiskPath:            opts.DiskPath,
		DiskSizeGB:          opts.DiskSizeGB,
		RawDisk:             opts.RawDisk,
		IPSWPath:            opts.IPSWPath,
		ISOPath:             opts.ISOPath,
		KernelPath:          opts.KernelPath,
		InitrdPath:          opts.InitrdPath,
		CmdLine:             opts.CmdLine,
		BootArgs:            opts.BootArgs,
		GUI:                 opts.GUI,
		Headless:            opts.Headless,
		RecoveryMode:        opts.RecoveryMode,
		ForceDFU:            opts.ForceDFU,
		StopIBoot1:          opts.StopIBoot1,
		StopIBoot2:          opts.StopIBoot2,
		ForceInstall:        opts.ForceInstall,
		SkipResume:          opts.SkipResume,
		Unattended:          opts.Unattended,
		InstallVM:           opts.InstallVM,
		NetworkMode:         opts.NetworkMode,
		HTTPListenAddr:      opts.HTTPListenAddr,
		SandboxLevel:        opts.SandboxLevel,
		HostContainment:     opts.HostContainment,
		EnableRosetta:       opts.EnableRosetta,
		EnableClipboard:     opts.EnableClipboard,
		SaveCompress:        opts.SaveCompress,
		SaveEncrypt:         opts.SaveEncrypt,
		GDBAddress:          opts.GDBAddress,
		GDBListenAll:        opts.GDBListenAll,
		LinuxNested:         opts.LinuxNested,
		LinuxNVMe:           opts.LinuxNVMe,
		LinuxShell:          opts.LinuxShell,
		WindowsGraphicsMode: opts.WindowsGraphicsMode,
		WindowsSerialMode:   opts.WindowsSerialMode,
		WindowsEFIRomPath:   opts.WindowsEFIRomPath,
		ProvisionUser:       opts.ProvisionUser,
		ProvisionPassword:   opts.ProvisionPassword,
		ProvisionAdmin:      opts.ProvisionAdmin,
		ProvisionStrategy:   opts.ProvisionStrategy,
		SerialOutput:        opts.SerialOutput,
		StartTimeout:        opts.StartTimeout,
		BootCommandsFile:    opts.BootCommandsFile,
	}
	for _, d := range opts.Displays {
		rc.Displays = append(rc.Displays, vmrun.DisplaySpec{
			Width:  d.Width,
			Height: d.Height,
			PPI:    d.PPI,
		})
	}
	for _, u := range opts.USBDevices {
		rc.USB = append(rc.USB, vmrun.USBSpec{
			Path:     u.Path,
			ReadOnly: u.ReadOnly,
		})
	}
	for _, b := range opts.BlockDevices {
		rc.BlockDevices = append(rc.BlockDevices, vmrun.BlockSpec{
			Path:     b.Path,
			ReadOnly: b.ReadOnly,
			Cache:    b.Sync,
		})
	}
	for _, v := range opts.Volumes {
		rc.Volumes = append(rc.Volumes, vmrun.VolumeMount{
			HostPath: v.HostPath,
			Tag:      v.Tag,
			ReadOnly: v.ReadOnly,
		})
	}
	for _, p := range opts.StartupPortForwards {
		rc.StartupForwards = append(rc.StartupForwards, vmrun.PortForward{
			HostPort:  p.HostPort,
			GuestPort: p.GuestPort,
		})
	}
	return rc
}

// vmrunHostConfig snapshots host-side context globals into a vmrun.HostConfig.
// VMDir falls back to the current selection if the -vm-dir override is empty.
func vmrunHostConfig() vmrun.HostConfig {
	return currentRuntimeOptions().vmrunHostConfig()
}

func (opts runtimeOptions) vmrunHostConfig() vmrun.HostConfig {
	target := currentVMSelection()
	dir := opts.VMDir
	if dir == "" {
		dir = target.Directory
	}
	return vmrun.HostConfig{
		VMDir:                dir,
		VMName:               opts.VMName,
		RuntimeProfile:       opts.RuntimeProfile,
		LaunchOrder:          opts.LaunchOrder,
		SandboxLevel:         opts.SandboxLevel,
		HostContainment:      opts.HostContainment,
		AutoMountVolumes:     opts.AutoMountVolumes,
		AutoUpgradeAgent:     opts.AutoUpgradeAgent,
		RecoverIdentity:      opts.RecoverIdentity,
		PreferPasswordDialog: opts.PreferPasswordDialog,
		Verbose:              opts.Verbose,
	}
}

func currentMacOSRunAndHostConfig() (vmrun.RunConfig, vmrun.HostConfig) {
	return vmrunRunConfig(vmrun.GuestMacOS), vmrunHostConfig()
}

func currentLinuxRunAndHostConfig() (vmrun.RunConfig, vmrun.HostConfig) {
	return vmrunRunConfig(vmrun.GuestLinux), vmrunHostConfig()
}

func currentWindowsRunAndHostConfig() (vmrun.RunConfig, vmrun.HostConfig) {
	return vmrunRunConfig(vmrun.GuestWindows), vmrunHostConfig()
}

func runMacOSVM() error {
	rc, hc := currentMacOSRunAndHostConfig()
	return runMacOSVMWithConfig(rc, hc, nil, nil)
}

func runLinuxVM() error {
	rc, hc := currentLinuxRunAndHostConfig()
	return runLinuxVMWithConfig(rc, hc, nil, nil)
}

func runWindowsVM() error {
	rc, hc := currentWindowsRunAndHostConfig()
	return runWindowsVMWithConfig(rc, hc, nil, nil)
}

func buildVMConfiguration(diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	rc, hc := currentMacOSRunAndHostConfig()
	return buildVMConfigurationWithConfig(rc, hc, diskImagePath)
}

func buildLinuxVMConfiguration(rc vmrun.RunConfig, diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	_, hc := currentLinuxRunAndHostConfig()
	return buildLinuxVMConfigurationWithConfig(rc, hc, diskImagePath)
}

func buildWindowsVMConfiguration(diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	rc, hc := currentWindowsRunAndHostConfig()
	return buildWindowsVMConfigurationWithConfig(rc, hc, diskImagePath)
}
