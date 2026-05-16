package main

import (
	"github.com/tmc/vz-macos/internal/vmrun"
)

// vmrunRunConfig snapshots the relevant package-level globals into a
// vmrun.RunConfig for the given guest OS. Slice 5 of design 039 introduces
// this boundary so entry points (macos.go, linux.go, windows.go) read run
// state from a value rather than directly from globals.
//
// Slice 5 keeps flag parsing in main.go untouched; later slices retire the
// globals once a thicker command environment owns the values.
func vmrunRunConfig(os vmrun.GuestOS) vmrun.RunConfig {
	rc := vmrun.RunConfig{
		OS:                  os,
		CPUCount:            cpuCount,
		MemoryGB:            memoryGB,
		DiskPath:            diskPath,
		DiskSizeGB:          diskSizeGB,
		RawDisk:             rawDisk,
		IPSWPath:            ipswPath,
		ISOPath:             isoPath,
		KernelPath:          kernelPath,
		InitrdPath:          initrdPath,
		CmdLine:             cmdLine,
		BootArgs:            bootArgs,
		GUI:                 guiMode,
		Headless:            headlessMode,
		RecoveryMode:        recoveryMode,
		ForceInstall:        forceInstall,
		SkipResume:          skipResume,
		Unattended:          unattended,
		NetworkMode:         networkMode,
		HTTPListenAddr:      runHTTPAddr,
		EnableRosetta:       enableRosetta,
		EnableClipboard:     enableClipboard,
		LinuxNested:         linuxNested,
		LinuxNVMe:           linuxNVMe,
		LinuxShell:          linuxShell,
		WindowsGraphicsMode: windowsGraphicsMode,
		WindowsSerialMode:   windowsSerialMode,
		WindowsEFIRomPath:   windowsEFIRomPath,
		ProvisionUser:       provisionUser,
		ProvisionPassword:   provisionPassword,
		ProvisionStrategy:   provisionStrategy,
		SerialOutput:        serialOutput,
		StartTimeout:        startTimeout,
		BootCommandsFile:    bootCommandsFile,
	}
	for _, d := range displays {
		rc.Displays = append(rc.Displays, vmrun.DisplaySpec{
			Width:  d.Width,
			Height: d.Height,
			PPI:    d.PPI,
		})
	}
	for _, u := range usbDevices {
		rc.USB = append(rc.USB, vmrun.USBSpec{
			Path:     u.Path,
			ReadOnly: u.ReadOnly,
		})
	}
	for _, b := range blockDevices {
		rc.BlockDevices = append(rc.BlockDevices, vmrun.BlockSpec{
			Path:     b.Path,
			ReadOnly: b.ReadOnly,
			Cache:    b.Sync,
		})
	}
	for _, v := range volumes {
		rc.Volumes = append(rc.Volumes, vmrun.VolumeMount{
			HostPath: v.HostPath,
			Tag:      v.Tag,
			ReadOnly: v.ReadOnly,
		})
	}
	for _, p := range startupPortForwards {
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
	target := currentVMSelection()
	dir := vmDir
	if dir == "" {
		dir = target.Directory
	}
	return vmrun.HostConfig{
		VMDir:                dir,
		VMName:               vmName,
		RuntimeProfile:       runtimeProfile,
		LaunchOrder:          launchOrder,
		AutoMountVolumes:     autoMountVolumes,
		AutoUpgradeAgent:     autoUpgradeAgent,
		RecoverIdentity:      recoverIdentity,
		PreferPasswordDialog: preferPasswordDialog,
		Verbose:              verbose,
	}
}
