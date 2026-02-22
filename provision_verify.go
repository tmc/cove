package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// VerifyResult holds the verification status for a single file
type VerifyResult struct {
	Path     string
	Exists   bool
	OwnerUID int
	OwnerGID int
	Mode     os.FileMode
	Expected string // expected ownership like "root:wheel"
	Status   string // "OK", "MISSING", "WRONG_OWNER"
}

// handleVerify verifies provisioning files in a VM disk
func handleVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	verboseFlag := fs.Bool("v", false, "Verbose output")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos verify [options]

Verify that provisioning files are correctly installed in the VM disk image.
This checks file existence, ownership, and permissions.

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Example:
  vz-macos -vm test-vm verify
  vz-macos -vm test-vm verify -v
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *verboseFlag {
		provisionVerbose = true
	}

	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s\nRun 'vz-macos install' first to create a VM", diskPath)
	}

	// Check disk is not already mounted/in use
	if err := checkDiskNotMounted(diskPath); err != nil {
		return err
	}

	fmt.Println("=== Verifying Provisioning Files ===")
	fmt.Printf("VM: %s\n\n", vmDir)

	// Mount the disk
	mountPoint, device, _, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount data volume: %w", err)
	}
	defer detachDisk(device)

	// Define files to verify with expected ownership
	filesToVerify := []struct {
		relativePath string
		expected     string
		required     bool
		description  string
	}{
		{"Library/LaunchDaemons/com.vz.provision.plist", "root:wheel", true, "LaunchDaemon plist"},
		{"private/var/db/vz-provision.sh", "root:wheel", true, "Provisioning script"},
		{"private/var/db/.AppleSetupDone", "any", false, "Setup Assistant skip marker"},
		{"private/etc/kcpassword", "root:wheel", false, "Auto-login password (kcpassword)"},
		{"Library/Preferences/com.apple.loginwindow.plist", "root:wheel", false, "Login window preferences"},
		{"private/var/db/.vz-provisioned", "any", false, "Provisioning completed marker"},
		{"private/var/db/vz-guest-tools.pkg", "root:wheel", false, "SPICE guest tools package (pending install)"},
		{"private/var/db/.vz-guest-tools-installed", "any", false, "SPICE guest tools installed marker"},
	}

	var results []VerifyResult
	allOK := true
	criticalFail := false

	for _, f := range filesToVerify {
		fullPath := filepath.Join(mountPoint, f.relativePath)
		result := VerifyResult{
			Path:     f.relativePath,
			Expected: f.expected,
		}

		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			result.Exists = false
			if f.required {
				result.Status = "MISSING (required)"
				criticalFail = true
			} else {
				result.Status = "not present"
			}
		} else if err != nil {
			result.Status = fmt.Sprintf("error: %v", err)
			allOK = false
		} else {
			result.Exists = true
			result.Mode = info.Mode()

			// Get ownership info
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				result.OwnerUID = int(stat.Uid)
				result.OwnerGID = int(stat.Gid)

				// Check ownership
				if f.expected == "root:wheel" {
					if result.OwnerUID == 0 && result.OwnerGID == 0 {
						result.Status = "OK"
					} else {
						result.Status = fmt.Sprintf("WRONG_OWNER (uid=%d gid=%d, need root:wheel)", result.OwnerUID, result.OwnerGID)
						allOK = false
						if f.required {
							criticalFail = true
						}
					}
				} else {
					result.Status = fmt.Sprintf("OK (uid=%d gid=%d)", result.OwnerUID, result.OwnerGID)
				}
			} else {
				result.Status = "OK (ownership check unavailable)"
			}
		}

		results = append(results, result)
	}

	// Print results
	fmt.Println("File Verification Results:")
	fmt.Println(strings.Repeat("-", 80))
	for _, r := range results {
		statusIcon := "✓"
		if strings.HasPrefix(r.Status, "MISSING") || strings.HasPrefix(r.Status, "WRONG_OWNER") {
			statusIcon = "✗"
		} else if r.Status == "not present" {
			statusIcon = "-"
		}
		fmt.Printf("%s %s\n", statusIcon, r.Path)
		fmt.Printf("    Status: %s\n", r.Status)
	}
	fmt.Println(strings.Repeat("-", 80))

	// Summary
	fmt.Println()
	if criticalFail {
		fmt.Println("❌ VERIFICATION FAILED: Critical files missing or have wrong ownership")
		fmt.Println()
		fmt.Println("To fix ownership issues, re-run provision:")
		fmt.Println("  ./vz-macos provision -user <user> -password <pass> -skip-setup-assistant")
		return fmt.Errorf("verification failed")
	} else if !allOK {
		fmt.Println("⚠️  VERIFICATION WARNING: Some non-critical issues found")
		fmt.Println("   Auto-login may not work, but user provisioning should succeed")
	} else {
		fmt.Println("✓ VERIFICATION PASSED: All files present with correct ownership")
	}

	// Check for completed provisioning
	provisionedPath := filepath.Join(mountPoint, "private", "var", "db", ".vz-provisioned")
	if _, err := os.Stat(provisionedPath); err == nil {
		fmt.Println()
		fmt.Println("Note: Provisioning has already completed (found .vz-provisioned marker)")
	}

	return nil
}
