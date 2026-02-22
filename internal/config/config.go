package config

// Global configuration variables shared across packages
var (
	// VM Configuration
	CPUCount        uint   = 2
	MemoryGB        uint64 = 4
	DiskSizeGB      uint64 = 64
	NetworkMode     string = "nat"
	EnableClipboard bool   = true
	EnableRosetta   bool   = false // For Linux VMs

	// Paths
	KernelPath string
	InitrdPath string
	ISOPath    string
	IPSWPath   string
	DiskPath   string
	VMDir      string

	// Flags
	SerialOutput     bool
	SerialOutputDest string // File path or 'stdout'
	Verbose          bool
	Headless         bool
	GUIMode          bool
	RecoveryMode     bool
	LinuxMode        bool
	CmdLine          string // Linux kernel command line
	BootArgs         string

	// Provisioning
	ProvisionUser     string
	ProvisionPassword string
	ProvisionAdmin    bool

	// Scripts
	EnableScripts   bool
	ScriptsPath     string
	ScriptsReadOnly bool
	ScriptsRunOnBoot bool
)
