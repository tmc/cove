// template.go - VM template save/create functionality
//
// Templates allow saving a configured VM as a reusable base for creating new VMs.
// Two storage modes are supported:
//
//   - Compressed (default): disk.img is gzip compressed for space efficiency
//   - Fast (clonefile): disk.img is stored uncompressed, enabling instant CoW creation
//
// The fast mode uses APFS clonefile which creates a copy-on-write clone that
// initially takes no additional disk space and only grows as blocks differ.
package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	"golang.org/x/sys/unix"
)

// ErrTemplateSourceNotFound is returned by SaveTemplateWithOptions when
// the source VM directory does not exist or fails vmconfig.Validate.
var ErrTemplateSourceNotFound = errors.New("template source VM not found")

// ErrTemplateExists is returned by SaveTemplateWithOptions when the
// target template directory already exists. Callers can branch on this
// with errors.Is to offer overwrite/rename guidance.
var ErrTemplateExists = errors.New("template already exists")

// ErrTemplateNotFound is returned by DeleteTemplate and
// CreateFromTemplate when the named template directory is missing or
// fails the layout/manifest check.
var ErrTemplateNotFound = errors.New("template not found")

// TemplateInfo holds information about a template.
type TemplateInfo struct {
	Name     string    // Template name (directory name)
	Path     string    // Full path to template directory
	DiskSize int64     // Disk image size (compressed or uncompressed)
	Created  time.Time // Creation time
	FastMode bool      // True if template uses clonefile (uncompressed)
}

// SaveTemplateOptions configures template creation behavior.
type SaveTemplateOptions struct {
	VMName       string // Source VM name
	TemplateName string // Template name to create
	FastMode     bool   // Use clonefile instead of compression (instant but more disk space)
}

// TemplateFiles are the files that make up a compressed template.
var TemplateFiles = []string{
	"disk.img.gz", // Compressed disk image
	"aux.img",     // NVRAM (not compressed, small)
	"hw.model",    // Hardware model (not compressed, tiny)
}

// TemplateFilesFast are the files that make up a fast (clonefile) template.
var TemplateFilesFast = []string{
	"disk.img", // Uncompressed disk image (for clonefile)
	"aux.img",  // NVRAM
	"hw.model", // Hardware model
}

// TemplateMarkerFast is a marker file indicating fast mode template.
const TemplateMarkerFast = ".fast-template"

// TemplateHashFile stores a hash of provisioning source files used to detect
// stale templates. The hash covers provision Go files, templates, and vzscripts.
const TemplateHashFile = ".source-hash"

// SaveTemplate saves a VM as a template (compresses disk image).
// This is a convenience wrapper around SaveTemplateWithOptions.
func SaveTemplate(vmName, templateName string) error {
	return SaveTemplateWithOptions(SaveTemplateOptions{
		VMName:       vmName,
		TemplateName: templateName,
		FastMode:     false,
	})
}

// SaveTemplateFast saves a VM as a fast template using clonefile.
// Fast templates use more disk space but enable instant VM creation via CoW.
func SaveTemplateFast(vmName, templateName string) error {
	return SaveTemplateWithOptions(SaveTemplateOptions{
		VMName:       vmName,
		TemplateName: templateName,
		FastMode:     true,
	})
}

// SaveTemplateWithOptions saves a VM as a template with configurable options.
func SaveTemplateWithOptions(opts SaveTemplateOptions) error {
	// Validate source VM
	vmPath := vmconfig.Path(opts.VMName)
	if !vmconfig.Validate(vmPath) {
		return fmt.Errorf("%w: %s", ErrTemplateSourceNotFound, opts.VMName)
	}

	// Check template doesn't exist
	templatePath := filepath.Join(vmconfig.TemplateDir(), opts.TemplateName)
	if _, err := os.Stat(templatePath); !os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrTemplateExists, opts.TemplateName)
	}

	modeStr := "compressed"
	if opts.FastMode {
		modeStr = "fast (clonefile)"
	}
	fmt.Printf("Creating template '%s' from VM '%s' [%s]\n", opts.TemplateName, opts.VMName, modeStr)

	// Create template directory
	if err := os.MkdirAll(templatePath, 0755); err != nil {
		return fmt.Errorf("create template dir: %w", err)
	}

	// Copy aux.img (small, no compression needed)
	fmt.Println("  Copying aux.img...")
	auxSrc := filepath.Join(vmPath, "aux.img")
	auxDst := filepath.Join(templatePath, "aux.img")
	if err := copyFile(auxSrc, auxDst); err != nil {
		os.RemoveAll(templatePath)
		return fmt.Errorf("copy aux.img: %w", err)
	}

	// Copy hw.model (tiny, no compression needed)
	fmt.Println("  Copying hw.model...")
	hwSrc := filepath.Join(vmPath, "hw.model")
	hwDst := filepath.Join(templatePath, "hw.model")
	if err := copyFile(hwSrc, hwDst); err != nil {
		os.RemoveAll(templatePath)
		return fmt.Errorf("copy hw.model: %w", err)
	}

	// Handle disk.img based on mode
	diskSrc := filepath.Join(vmPath, "disk.img")
	if opts.FastMode {
		// Fast mode: use clonefile for instant CoW copy
		diskDst := filepath.Join(templatePath, "disk.img")
		fmt.Println("  Cloning disk.img (clonefile)...")
		if err := cloneFileWithFallback(diskSrc, diskDst); err != nil {
			os.RemoveAll(templatePath)
			return fmt.Errorf("clone disk.img: %w", err)
		}
		// Create fast mode marker
		markerPath := filepath.Join(templatePath, TemplateMarkerFast)
		if err := os.WriteFile(markerPath, []byte("fast-mode\n"), 0644); err != nil {
			os.RemoveAll(templatePath)
			return fmt.Errorf("create fast mode marker: %w", err)
		}
	} else {
		// Standard mode: compress for space efficiency
		diskDst := filepath.Join(templatePath, "disk.img.gz")
		fmt.Println("  Compressing disk.img (this may take a while)...")
		if err := compressFile(diskSrc, diskDst); err != nil {
			os.RemoveAll(templatePath)
			return fmt.Errorf("compress disk.img: %w", err)
		}
	}

	// Note: We intentionally don't copy machine.id - it's generated when creating from template

	// Write provisioning source hash for staleness detection.
	hashPath := filepath.Join(templatePath, TemplateHashFile)
	os.WriteFile(hashPath, []byte(ProvisioningSourceHash()), 0644)

	fmt.Println("Template created successfully.")
	return nil
}

// cloneFileWithFallback tries APFS clonefile first, falls back to regular copy.
func cloneFileWithFallback(src, dst string) error {
	err := unix.Clonefile(src, dst, 0)
	if err == nil {
		return nil
	}
	// Fall back to regular copy if clonefile fails (e.g., cross-filesystem)
	fmt.Printf("  (clonefile failed: %v, using regular copy)\n", err)
	return copyFile(src, dst)
}

// ListTemplates returns all available templates.
func ListTemplates() ([]TemplateInfo, error) {
	templateDir := vmconfig.TemplateDir()

	// Create template directory if it doesn't exist
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		return nil, fmt.Errorf("create template dir: %w", err)
	}

	entries, err := os.ReadDir(templateDir)
	if err != nil {
		return nil, fmt.Errorf("read template dir: %w", err)
	}

	var templates []TemplateInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		templatePath := filepath.Join(templateDir, entry.Name())
		info, err := getTemplateInfo(templatePath)
		if err != nil {
			continue // Skip invalid templates
		}

		templates = append(templates, *info)
	}

	// Sort by name
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})

	return templates, nil
}

// getTemplateInfo returns information about a template.
func getTemplateInfo(templatePath string) (*TemplateInfo, error) {
	name := filepath.Base(templatePath)

	// Check if this is a fast mode template
	markerPath := filepath.Join(templatePath, TemplateMarkerFast)
	fastMode := false
	if _, err := os.Stat(markerPath); err == nil {
		fastMode = true
	}

	// Validate template has required files based on mode
	requiredFiles := TemplateFiles
	if fastMode {
		requiredFiles = TemplateFilesFast
	}

	for _, f := range requiredFiles {
		path := filepath.Join(templatePath, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, fmt.Errorf("missing required file: %s", f)
		}
	}

	// Get disk image info
	diskPath := filepath.Join(templatePath, "disk.img.gz")
	if fastMode {
		diskPath = filepath.Join(templatePath, "disk.img")
	}

	diskInfo, err := os.Stat(diskPath)
	if err != nil {
		return nil, fmt.Errorf("stat disk image: %w", err)
	}

	return &TemplateInfo{
		Name:     name,
		Path:     templatePath,
		DiskSize: diskInfo.Size(),
		Created:  diskInfo.ModTime(),
		FastMode: fastMode,
	}, nil
}

// CreateFromTemplateOptions configures VM creation from template.
type CreateFromTemplateOptions struct {
	TemplateName string // Source template name
	VMName       string // Target VM name
	UseClonefile bool   // Use clonefile even if template is compressed (decompress once, then clone)
}

// CreateFromTemplate creates a new VM from a template.
func CreateFromTemplate(templateName, vmName string) error {
	return CreateFromTemplateWithOptions(CreateFromTemplateOptions{
		TemplateName: templateName,
		VMName:       vmName,
	})
}

// CreateFromTemplateWithOptions creates a new VM from a template with configurable options.
func CreateFromTemplateWithOptions(opts CreateFromTemplateOptions) error {
	// Validate template exists
	templatePath := filepath.Join(vmconfig.TemplateDir(), opts.TemplateName)
	templateInfo, err := getTemplateInfo(templatePath)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrTemplateNotFound, opts.TemplateName, err)
	}

	// Warn if template was built from different provisioning sources.
	if stale, tmplHash, curHash := CheckTemplateStale(templatePath); stale {
		fmt.Fprintf(os.Stderr, "warning: template %q is stale (hash %s, current %s) — run 'make golden' to rebuild\n",
			opts.TemplateName, tmplHash, curHash)
	}

	// Check VM doesn't exist
	vmPath := vmconfig.Path(opts.VMName)
	if _, err := os.Stat(vmPath); !os.IsNotExist(err) {
		return fmt.Errorf("vm already exists: %s", opts.VMName)
	}

	modeStr := "decompressing"
	if templateInfo.FastMode {
		modeStr = "cloning (CoW)"
	}
	fmt.Printf("Creating VM '%s' from template '%s' [%s]\n", opts.VMName, opts.TemplateName, modeStr)

	// Create VM directory
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("create VM dir: %w", err)
	}

	// Copy aux.img
	fmt.Println("  Copying aux.img...")
	auxSrc := filepath.Join(templatePath, "aux.img")
	auxDst := filepath.Join(vmPath, "aux.img")
	if err := copyFile(auxSrc, auxDst); err != nil {
		os.RemoveAll(vmPath)
		return fmt.Errorf("copy aux.img: %w", err)
	}

	// Copy hw.model
	fmt.Println("  Copying hw.model...")
	hwSrc := filepath.Join(templatePath, "hw.model")
	hwDst := filepath.Join(vmPath, "hw.model")
	if err := copyFile(hwSrc, hwDst); err != nil {
		os.RemoveAll(vmPath)
		return fmt.Errorf("copy hw.model: %w", err)
	}

	// Handle disk.img based on template mode
	diskDst := filepath.Join(vmPath, "disk.img")
	if templateInfo.FastMode {
		// Fast mode template: use clonefile for instant CoW copy
		diskSrc := filepath.Join(templatePath, "disk.img")
		fmt.Println("  Cloning disk.img (clonefile)...")
		if err := cloneFileWithFallback(diskSrc, diskDst); err != nil {
			os.RemoveAll(vmPath)
			return fmt.Errorf("clone disk.img: %w", err)
		}
	} else {
		// Compressed template: decompress
		diskSrc := filepath.Join(templatePath, "disk.img.gz")
		fmt.Println("  Decompressing disk.img (this may take a while)...")
		if err := decompressFile(diskSrc, diskDst); err != nil {
			os.RemoveAll(vmPath)
			return fmt.Errorf("decompress disk.img: %w", err)
		}
	}

	// Generate new machine ID
	fmt.Println("  Generating new machine ID...")
	if err := generateMachineID(vmPath); err != nil {
		os.RemoveAll(vmPath)
		return fmt.Errorf("generate machine ID: %w", err)
	}

	fmt.Println("VM created successfully.")
	return nil
}

// DeleteTemplate deletes a template.
func DeleteTemplate(templateName string) error {
	templatePath := filepath.Join(vmconfig.TemplateDir(), templateName)

	// Verify it's a valid template
	if _, err := getTemplateInfo(templatePath); err != nil {
		return fmt.Errorf("%w: %s", ErrTemplateNotFound, templateName)
	}

	return os.RemoveAll(templatePath)
}

// compressFile compresses a file using gzip.
func compressFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Use best compression for templates (slower but smaller)
	gzWriter, err := gzip.NewWriterLevel(dstFile, gzip.BestCompression)
	if err != nil {
		return err
	}
	defer gzWriter.Close()

	// Show progress during compression
	srcSize := srcInfo.Size()
	reader := &progressReader{
		reader:    srcFile,
		total:     srcSize,
		operation: "Compressing",
	}

	_, err = io.Copy(gzWriter, reader)
	fmt.Println() // Clear progress line
	return err
}

// decompressFile decompresses a gzip file.
func decompressFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	gzReader, err := gzip.NewReader(srcFile)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, gzReader)
	return err
}

// progressReader wraps an io.Reader to show progress.
type progressReader struct {
	reader    io.Reader
	total     int64
	read      int64
	operation string
	lastPrint time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)

	// Print progress every 100ms
	if time.Since(pr.lastPrint) > 100*time.Millisecond {
		pct := float64(pr.read) / float64(pr.total) * 100
		fmt.Printf("\r  %s: %.1f%%", pr.operation, pct)
		pr.lastPrint = time.Now()
	}

	return n, err
}

// provisioningSourceFiles lists files whose content determines the template's
// provisioning behavior. Changes to any of these files make existing templates
// stale.
var provisioningSourceFiles = []string{
	"provision.go",
	"provision_inject.go",
	"provision_templates.go",
	"provision_cli.go",
	"provision_mount.go",
	"agent_inject.go",
	"templates/vz-provision.sh.tmpl",
	"templates/vz-autologin.sh.tmpl",
	"templates/com.github.tmc.vz-macos.provision.plist",
	"templates/com.github.tmc.vz-macos.autologin.plist",
	"templates/com.github.tmc.vz-macos.vz-agent.plist",
	"templates/com.github.tmc.vz-macos.vz-agent-user.plist",
}

// ProvisioningSourceHash computes a short SHA-256 hash of the provisioning
// source files. The hash is stable across builds as long as the file contents
// don't change.
func ProvisioningSourceHash() string {
	h := sha256.New()
	for _, f := range provisioningSourceFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// CheckTemplateStale compares the template's stored source hash with the
// current provisioning source hash. Returns true if the template was built
// from different source files.
func CheckTemplateStale(templatePath string) (stale bool, templateHash, currentHash string) {
	hashPath := filepath.Join(templatePath, TemplateHashFile)
	data, err := os.ReadFile(hashPath)
	if err != nil {
		return false, "", "" // no hash file, can't determine staleness
	}
	templateHash = string(data)
	currentHash = ProvisioningSourceHash()
	return templateHash != currentHash, templateHash, currentHash
}
