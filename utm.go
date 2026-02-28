// UTM bundle support for vz-macos.
// Allows running existing UTM Apple VMs without modification.
//
// UTM bundle structure (.utm directory):
//
//	MyVM.utm/
//	├── config.plist           # PropertyList XML config
//	├── screenshot.png         # Optional thumbnail
//	└── Data/                  # All VM data files
//	    ├── disk.img           # Main disk image
//	    ├── AuxiliaryStorage   # macOS NVRAM
//	    └── vm.vzsave          # Saved VM state (macOS 14+)
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/apple/x/plist"
)

// UTMConfig represents the parsed UTM configuration.
type UTMConfig struct {
	Backend    string
	Name       string
	CPUCount   uint
	MemorySize uint64 // in bytes
	DiskPath   string
	HWModel    []byte // VZMacHardwareModel data representation
	MachineID  []byte // VZMacMachineIdentifier data representation
	AuxStorage string // path to auxiliary storage
	Displays   []DisplayConfig
	MACAddress string
}

// LoadUTMBundle loads a UTM bundle from the given path.
// Returns an error if the bundle is not an Apple VM or is invalid.
func LoadUTMBundle(bundlePath string) (*UTMConfig, error) {
	// Check bundle exists
	info, err := os.Stat(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("bundle not found: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bundle must be a directory: %s", bundlePath)
	}

	// Parse config.plist
	configPath := filepath.Join(bundlePath, "config.plist")
	pl, err := plist.ParseFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("parse config.plist: %w", err)
	}

	// Check backend is Apple
	backend := plist.String(pl, "Backend")
	if backend != "Apple" {
		return nil, fmt.Errorf("only Apple backend supported, got: %q", backend)
	}

	// Check operating system is macOS (not Linux)
	osType := plist.String(pl, "System", "Boot", "OperatingSystem")
	if osType != "" && osType != "macOS" {
		return nil, fmt.Errorf("only macOS guests supported, got: %q", osType)
	}

	// Get VM name
	name := plist.String(pl, "Information", "Name")
	if name == "" {
		name = filepath.Base(bundlePath)
	}

	// Get system configuration
	cpuCount := plist.Int(pl, "System", "CPUCount")
	if cpuCount == 0 {
		// UTM uses 0 to mean "use all available CPUs"
		cpuCount = runtime.NumCPU()
	}

	memorySize := plist.Int(pl, "System", "MemorySize")
	if memorySize == 0 {
		memorySize = 4096 // default 4GB in MiB
	}

	// Get Mac platform data
	hwModel := plist.Bytes(pl, "System", "MacPlatform", "HardwareModel")
	machineID := plist.Bytes(pl, "System", "MacPlatform", "MachineIdentifier")
	auxStoragePath := plist.String(pl, "System", "MacPlatform", "AuxiliaryStoragePath")

	// Get display configuration
	var parsedDisplays []DisplayConfig
	displayArray := plist.Array(pl, "Display")
	for _, d := range displayArray {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		width := plist.Int(dm, "WidthPixels")
		height := plist.Int(dm, "HeightPixels")
		ppi := plist.Int(dm, "PixelsPerInch")
		if width > 0 && height > 0 {
			if ppi == 0 {
				ppi = 144
			}
			parsedDisplays = append(parsedDisplays, DisplayConfig{
				Width:  width,
				Height: height,
				PPI:    ppi,
			})
		}
	}

	// Get network MAC address
	var macAddress string
	networkArray := plist.Array(pl, "Network")
	if len(networkArray) > 0 {
		if net, ok := networkArray[0].(map[string]any); ok {
			macAddress = plist.String(net, "MacAddress")
		}
	}

	// Get drive configuration
	drives := plist.Array(pl, "Drive")
	var diskImageName string
	if len(drives) > 0 {
		if drive, ok := drives[0].(map[string]any); ok {
			if name, ok := drive["ImageName"].(string); ok {
				diskImageName = name
			}
		}
	}

	// Build paths relative to Data/ directory
	dataDir := filepath.Join(bundlePath, "Data")
	diskPath := ""
	if diskImageName != "" {
		diskPath = filepath.Join(dataDir, diskImageName)
	}
	auxStorage := ""
	if auxStoragePath != "" {
		auxStorage = filepath.Join(dataDir, auxStoragePath)
	}

	// Validate required files exist
	if diskPath != "" {
		if _, err := os.Stat(diskPath); err != nil {
			return nil, fmt.Errorf("disk image not found: %s", diskPath)
		}
	}
	if auxStorage != "" {
		if _, err := os.Stat(auxStorage); err != nil {
			return nil, fmt.Errorf("auxiliary storage not found: %s", auxStorage)
		}
	}

	return &UTMConfig{
		Backend:    backend,
		Name:       name,
		CPUCount:   uint(cpuCount),
		MemorySize: uint64(memorySize) * 1024 * 1024, // MiB to bytes
		DiskPath:   diskPath,
		HWModel:    hwModel,
		MachineID:  machineID,
		AuxStorage: auxStorage,
		Displays:   parsedDisplays,
		MACAddress: macAddress,
	}, nil
}

// runUTMBundle runs a VM from a UTM bundle.
func runUTMBundle(bundlePath string) error {
	fmt.Printf("=== Loading UTM Bundle ===\n")
	fmt.Printf("Path: %s\n", bundlePath)

	cfg, err := LoadUTMBundle(bundlePath)
	if err != nil {
		return fmt.Errorf("load UTM bundle: %w", err)
	}

	fmt.Printf("Name: %s\n", cfg.Name)
	fmt.Printf("CPUs: %d\n", cfg.CPUCount)
	fmt.Printf("Memory: %d GB\n", cfg.MemorySize/(1024*1024*1024))
	fmt.Printf("Disk: %s\n", cfg.DiskPath)
	fmt.Printf("Aux Storage: %s\n", cfg.AuxStorage)

	// Override global settings with UTM config
	cpuCount = cfg.CPUCount
	memoryGB = cfg.MemorySize / (1024 * 1024 * 1024)
	diskPath = cfg.DiskPath

	// Set vmDir to the Data directory for auxiliary storage lookup
	vmDir = filepath.Join(bundlePath, "Data")

	// Apply display configuration from UTM bundle
	if len(cfg.Displays) > 0 {
		displays = DisplaySlice(cfg.Displays)
	}

	// Apply MAC address from UTM bundle so DHCP leases are preserved
	if cfg.MACAddress != "" {
		macPath := filepath.Join(vmDir, "mac.address")
		if err := os.WriteFile(macPath, []byte(cfg.MACAddress+"\n"), 0644); err != nil {
			fmt.Printf("Warning: could not save MAC address: %v\n", err)
		}
	}

	// Apply auxiliary storage path from UTM bundle
	if cfg.AuxStorage != "" {
		utmAuxStoragePath = cfg.AuxStorage
	}

	// If we have hardware model and machine ID from UTM, save them
	// so buildVMConfiguration can load them
	if len(cfg.HWModel) > 0 {
		hwPath := filepath.Join(vmDir, "hw.model")
		if err := os.WriteFile(hwPath, cfg.HWModel, 0644); err != nil {
			fmt.Printf("Warning: could not save hardware model: %v\n", err)
		}
	}
	if len(cfg.MachineID) > 0 {
		machineIDPath := filepath.Join(vmDir, "machine.id")
		if err := os.WriteFile(machineIDPath, cfg.MachineID, 0644); err != nil {
			fmt.Printf("Warning: could not save machine ID: %v\n", err)
		}
	}

	// Now run using existing macOS VM logic
	return runMacOSVM()
}

// UTMVMInfo holds basic info about a discovered UTM VM.
type UTMVMInfo struct {
	Path    string
	Name    string
	Backend string
	CPUs    int
	MemGB   int
}

// utmSearchPaths returns paths to search for UTM bundles.
func utmSearchPaths() []string {
	home, _ := os.UserHomeDir()
	paths := []string{
		// Sandboxed UTM (App Store)
		filepath.Join(home, "Library/Containers/com.utmapp.UTM/Data/Documents"),
		// Sandboxed UTM-SE (GitHub version)
		filepath.Join(home, "Library/Containers/com.utmapp.UTM-SE/Data/Documents"),
		// Non-sandboxed UTM
		filepath.Join(home, "Library/Application Support/UTM"),
		// Common user locations
		filepath.Join(home, "Documents/UTM"),
		filepath.Join(home, "Documents"),
		filepath.Join(home, "Desktop"),
		// Current directory
		".",
	}
	return paths
}

// discoverFromUTMRegistry reads UTM's UserDefaults to find registered VMs.
func discoverFromUTMRegistry() []string {
	var paths []string

	// Try both UTM bundle IDs (App Store and GitHub versions)
	for _, bundleID := range []string{"com.utmapp.UTM", "com.utmapp.UTM-SE"} {
		cmd := exec.Command("defaults", "read", bundleID, "Registry")
		output, err := cmd.Output()
		if err != nil {
			continue
		}
		paths = append(paths, parseRegistryPaths(string(output))...)
	}

	return paths
}

// parseRegistryPaths extracts VM paths from UTM registry output.
func parseRegistryPaths(output string) []string {
	var paths []string

	// Parse the plist output - look for Path entries
	// Format: "Path" = "/path/to/VM.utm";
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, `"Path"`) || strings.Contains(line, `Path =`) {
			// Extract path from: "Path" = "/path/to/VM.utm";
			// or: Path = "/path/to/VM.utm";
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				path := strings.TrimSpace(parts[1])
				path = strings.Trim(path, `";`)
				path = strings.Trim(path, `"`)
				if strings.HasSuffix(path, ".utm") {
					// Verify it exists
					if _, err := os.Stat(path); err == nil {
						paths = append(paths, path)
					}
				}
			}
		}
	}

	return paths
}

// findUTMBundles searches for .utm bundles in common locations and UTM's registry.
func findUTMBundles() []UTMVMInfo {
	var vms []UTMVMInfo
	seen := make(map[string]bool)

	// Helper to add a VM bundle
	addBundle := func(bundlePath string) {
		absPath, _ := filepath.Abs(bundlePath)
		if seen[absPath] {
			return
		}
		seen[absPath] = true

		// Try to parse config.plist for info
		configPath := filepath.Join(bundlePath, "config.plist")
		pl, err := plist.ParseFile(configPath)
		if err != nil {
			return
		}

		backend := plist.String(pl, "Backend")
		name := plist.String(pl, "Information", "Name")
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(bundlePath), ".utm")
		}
		cpus := plist.Int(pl, "System", "CPUCount")
		memMB := plist.Int(pl, "System", "MemorySize")

		vms = append(vms, UTMVMInfo{
			Path:    bundlePath,
			Name:    name,
			Backend: backend,
			CPUs:    cpus,
			MemGB:   memMB / 1024,
		})
	}

	// First, check UTM's registry for registered VMs
	for _, path := range discoverFromUTMRegistry() {
		addBundle(path)
	}

	// Then search common directories
	for _, searchPath := range utmSearchPaths() {
		entries, err := os.ReadDir(searchPath)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".utm") {
				continue
			}
			addBundle(filepath.Join(searchPath, entry.Name()))
		}
	}

	// Sort by name
	sort.Slice(vms, func(i, j int) bool {
		return vms[i].Name < vms[j].Name
	})

	return vms
}

// discoverViaAppleScript queries UTM directly for registered VMs.
// Returns VM names and basic info. Requires Automation permission.
func discoverViaAppleScript() ([]UTMVMInfo, error) {
	// Get VM names, backends, and statuses via AppleScript
	script := `tell application "UTM"
	set vmList to {}
	repeat with vm in (every virtual machine)
		set vmName to name of vm
		set vmBackend to backend of vm as string
		set vmStatus to status of vm as string
		set end of vmList to vmName & "|" & vmBackend & "|" & vmStatus
	end repeat
	return vmList
end tell`

	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("AppleScript failed: %w (grant Automation permission in System Settings)", err)
	}

	var vms []UTMVMInfo
	// Parse output: "Name1|backend1|status1, Name2|backend2|status2, ..."
	output := strings.TrimSpace(string(out))
	if output == "" {
		return vms, nil
	}

	entries := strings.Split(output, ", ")
	for _, entry := range entries {
		parts := strings.Split(entry, "|")
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		backend := parts[1]

		// Map AppleScript backend enum to our format
		if strings.Contains(strings.ToLower(backend), "apple") {
			backend = "Apple"
		} else if strings.Contains(strings.ToLower(backend), "qemu") {
			backend = "QEMU"
		}

		vms = append(vms, UTMVMInfo{
			Name:    name,
			Backend: backend,
			// Path will be derived from name or needs export
		})
	}

	return vms, nil
}

// runUTMLauncher shows available UTM VMs and lets the user select one.
func runUTMLauncher() error {
	fmt.Println("=== UTM VM Launcher ===")
	fmt.Println("Searching for UTM bundles...")

	vms := findUTMBundles()

	// If no VMs found via filesystem, try AppleScript
	if len(vms) == 0 {
		fmt.Println("\nNo VMs found via filesystem. Trying AppleScript...")
		asVMs, err := discoverViaAppleScript()
		if err != nil {
			fmt.Printf("AppleScript discovery failed: %v\n", err)
		} else if len(asVMs) > 0 {
			fmt.Printf("Found %d VM(s) via AppleScript (requires export to run):\n", len(asVMs))
			for i, vm := range asVMs {
				supported := "✓"
				if vm.Backend != "Apple" {
					supported = "✗"
				}
				fmt.Printf("  %s %d. %s (%s)\n", supported, i+1, vm.Name, vm.Backend)
			}
			fmt.Println("\nTo run sandboxed VMs, either:")
			fmt.Println("  1. Grant Full Disk Access to this app")
			fmt.Println("  2. Export VM from UTM to an accessible location")
			fmt.Println("  3. Use: ./vz-macos -utm /path/to/exported/VM.utm")
			return nil
		}
	}

	if len(vms) == 0 {
		fmt.Println("\nNo UTM bundles found in:")
		for _, p := range utmSearchPaths() {
			fmt.Printf("  - %s\n", p)
		}
		fmt.Println("\nOptions:")
		fmt.Println("  b - Browse for a .utm bundle")
		fmt.Println("  q - Quit")
		fmt.Print("\nChoice: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))

		if input == "b" {
			fmt.Println("Opening file picker...")
			path := showOpenPanelForUTM()
			if path == "" {
				fmt.Println("Cancelled.")
				return nil
			}
			if !strings.HasSuffix(path, ".utm") {
				return fmt.Errorf("selected path is not a .utm bundle: %s", path)
			}
			fmt.Printf("Selected: %s\n", path)
			return runUTMBundle(path)
		}
		return nil
	}

	fmt.Printf("\nFound %d UTM bundle(s):\n\n", len(vms))

	for i, vm := range vms {
		supported := ""
		if vm.Backend == "Apple" {
			supported = "✓"
		} else {
			supported = "✗"
		}

		memStr := fmt.Sprintf("%d GB", vm.MemGB)
		if vm.MemGB == 0 {
			memStr = "auto"
		}
		cpuStr := fmt.Sprintf("%d", vm.CPUs)
		if vm.CPUs == 0 {
			cpuStr = "auto"
		}

		fmt.Printf("  %s %d. %s\n", supported, i+1, vm.Name)
		fmt.Printf("       %s | %s CPUs | %s RAM\n", vm.Backend, cpuStr, memStr)
		fmt.Printf("       %s\n\n", vm.Path)
	}

	fmt.Println("Tip: Add -gui flag to run with display window")

	// Prompt for selection
	fmt.Print("\nEnter VM number, 'b' to browse, or 'q' to quit: ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "q" || input == "" {
		return nil
	}

	if strings.ToLower(input) == "b" {
		fmt.Println("Opening file picker...")
		path := showOpenPanelForUTM()
		if path == "" {
			fmt.Println("Cancelled.")
			return nil
		}
		if !strings.HasSuffix(path, ".utm") {
			return fmt.Errorf("selected path is not a .utm bundle: %s", path)
		}
		fmt.Printf("Selected: %s\n", path)
		return runUTMBundle(path)
	}

	num, err := strconv.Atoi(input)
	if err != nil || num < 1 || num > len(vms) {
		return fmt.Errorf("invalid selection: %s", input)
	}

	selected := vms[num-1]

	if selected.Backend != "Apple" {
		return fmt.Errorf("only Apple backend VMs supported (selected VM uses %s)", selected.Backend)
	}

	fmt.Printf("\nLaunching: %s\n\n", selected.Name)
	return runUTMBundle(selected.Path)
}
