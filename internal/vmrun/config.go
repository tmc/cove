package vmrun

import "time"

// GuestOS identifies the guest operating system family.
type GuestOS int

const (
	GuestUnknown GuestOS = iota
	GuestMacOS
	GuestLinux
	GuestWindows
)

func (g GuestOS) String() string {
	switch g {
	case GuestMacOS:
		return "macos"
	case GuestLinux:
		return "linux"
	case GuestWindows:
		return "windows"
	}
	return "unknown"
}

// RunConfig is the normalized user intent for a single VM run or install.
// It is populated from CLI flags and saved VM configuration before the VM
// is constructed. It carries no Virtualization.framework or AppKit handles.
type RunConfig struct {
	OS GuestOS

	// Hardware.
	CPUCount   uint
	MemoryGB   uint64
	DiskPath   string
	DiskSizeGB uint64
	RawDisk    bool

	// Boot artifacts.
	IPSWPath   string // macOS install only
	ISOPath    string // Linux/Windows install
	KernelPath string // Linux direct-kernel boot
	InitrdPath string // Linux direct-kernel boot
	CmdLine    string // Linux kernel command line
	BootArgs   string // saved next to the VM for the guest to read

	// Run-mode flags.
	GUI          bool
	Headless     bool
	RecoveryMode bool
	ForceInstall bool
	SkipResume   bool
	Unattended   bool

	// Display configuration. Width/height/PPI per scanout. An empty slice
	// means "use the entry point's default for this OS".
	Displays []DisplaySpec

	// Network.
	NetworkMode     string
	StartupForwards []PortForward
	HTTPListenAddr  string

	// Security policy.
	SandboxLevel    string
	HostContainment bool

	// Storage and shared folders.
	USB          []USBSpec
	BlockDevices []BlockSpec
	Volumes      []VolumeMount

	// Optional features.
	EnableRosetta   bool
	EnableClipboard bool

	// Linux-specific.
	LinuxNested bool
	LinuxNVMe   bool
	LinuxShell  bool

	// Windows-specific.
	WindowsGraphicsMode string
	WindowsSerialMode   string
	WindowsEFIRomPath   string

	// Provisioning intent. Applied by internal/provision; vmrun only
	// records the user's request.
	ProvisionUser     string
	ProvisionPassword string
	ProvisionStrategy string

	// Diagnostics and timing.
	SerialOutput     string // "stdout", "none", or a host file path
	StartTimeout     time.Duration
	BootCommandsFile string
}

// ResolveISO records a host-resolved ISO path on the receiver. It is the
// single sanctioned mutation entry for late ISO resolution: callers that
// need to download or auto-pick install media after the RunConfig has been
// constructed call ResolveISO so subsequent VM-build code reads the
// resolved value from rc.ISOPath rather than from a package-level global.
func (c *RunConfig) ResolveISO(path string) {
	if c == nil {
		return
	}
	c.ISOPath = path
}

// HostConfig is host-side context that does not vary per command invocation
// but does affect VM construction (paths, profiles, helper modes).
type HostConfig struct {
	VMDir  string
	VMName string

	RuntimeProfile string // "full" or "minimal"
	LaunchOrder    string // "window-first" or "start-first"

	SandboxLevel    string
	HostContainment bool

	AutoMountVolumes     bool
	AutoUpgradeAgent     bool
	RecoverIdentity      bool
	PreferPasswordDialog bool
	Verbose              bool
}

// DisplaySpec describes a single virtual display. PPI of zero means use the
// entry point's default for the guest OS.
type DisplaySpec struct {
	Width  int
	Height int
	PPI    int
}

// USBSpec describes a USB mass-storage attachment.
type USBSpec struct {
	Path     string
	ReadOnly bool
}

// BlockSpec describes a raw block device attachment.
type BlockSpec struct {
	Path     string
	ReadOnly bool
	Cache    string // "" defers to package main's default cache mode.
}

// VolumeMount is a host directory shared into the guest via VirtioFS.
type VolumeMount struct {
	HostPath string
	Tag      string
	ReadOnly bool
}

// PortForward is a startup host TCP -> guest vsock forward.
type PortForward struct {
	HostPort  int
	GuestPort uint32
}
