//go:build darwin

package ios

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/ios/bundle"
	"github.com/tmc/cove/internal/vmconfig"
)

// Configure updates a stopped iOS bundle while preserving other cove metadata.
// The caller must hold the bundle run lock. ROM paths may be absolute or relative
// to dir; their contents are staged under roms before publishing configuration.
func Configure(dir string, hardware vmconfig.Hardware, config bundle.Config) (err error) {
	current, err := vmconfig.Load(dir)
	if err != nil {
		return err
	}
	if current.IOS == nil {
		return fmt.Errorf("bundle is not an ios guest")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	if err := validateHardware(hardware.CPU, hardware.MemoryGB); err != nil {
		return err
	}

	var createdFiles []string
	defer func() {
		if err != nil {
			for _, path := range createdFiles {
				if cleanupErr := os.Remove(path); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
					err = fmt.Errorf("%w; remove staged rom: %v", err, cleanupErr)
				}
			}
		}
	}()
	for _, rom := range []*string{&config.ROM, &config.SEPROM} {
		if *rom == "" {
			continue
		}
		relative, created, stageErr := stageROM(dir, *rom)
		if stageErr != nil {
			return fmt.Errorf("stage ios rom: %w", stageErr)
		}
		*rom = relative
		if created {
			createdFiles = append(createdFiles, filepath.Join(dir, relative))
		}
	}
	current.CPU, current.MemoryGB, current.IOS = hardware.CPU, hardware.MemoryGB, &config
	return vmconfig.Save(dir, current)
}

func validateHardware(cpu uint, memoryGB uint64) error {
	if cpu == 0 || memoryGB == 0 || memoryGB > ^uint64(0)/(1<<30) {
		return fmt.Errorf("invalid ios cpu or memory")
	}
	limits := vz.GetVZVirtualMachineConfigurationClass()
	if cpu < limits.MinimumAllowedCPUCount() || cpu > limits.MaximumAllowedCPUCount() {
		return fmt.Errorf("ios cpu count must be between %d and %d", limits.MinimumAllowedCPUCount(), limits.MaximumAllowedCPUCount())
	}
	if memoryGB<<30 < limits.MinimumAllowedMemorySize() || memoryGB<<30 > limits.MaximumAllowedMemorySize() {
		return fmt.Errorf("ios memory must be between %d and %d bytes", limits.MinimumAllowedMemorySize(), limits.MaximumAllowedMemorySize())
	}
	return nil
}

func stageROM(dir, name string) (string, bool, error) {
	path, err := romFile(dir, name)
	if err != nil {
		return "", false, err
	}
	romDirAbs, err := filepath.Abs(filepath.Join(dir, "roms"))
	if err != nil {
		return "", false, err
	}
	// romFile already verified the content digest for this staged path.
	if filepath.Dir(path) == romDirAbs {
		return filepath.Join("roms", filepath.Base(path)), false, nil
	}
	input, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", false, fmt.Errorf("rom must be a nonempty regular file")
	}
	romDir := filepath.Join(dir, "roms")
	if err := os.Mkdir(romDir, 0700); err != nil && !os.IsExist(err) {
		return "", false, err
	}
	info, err = os.Lstat(romDir)
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("roms must be a directory, not a symlink or file")
	}
	temp, err := os.CreateTemp(romDir, ".stage-")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(temp, digest), input)
	if err != nil {
		return "", false, err
	}
	if size == 0 {
		return "", false, fmt.Errorf("empty rom")
	}
	if err := temp.Sync(); err != nil {
		return "", false, err
	}
	if err := temp.Close(); err != nil {
		return "", false, err
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	relative := filepath.Join("roms", hash+".rom")
	destination := filepath.Join(dir, relative)
	if err := os.Link(temp.Name(), destination); err != nil {
		if !os.IsExist(err) {
			return "", false, err
		}
		if err := checkROMDigest(destination, hash); err != nil {
			return "", false, err
		}
		return relative, false, nil
	}
	return relative, true, nil
}

func checkROMDigest(path, want string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged rom is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != want {
		return fmt.Errorf("staged rom digest mismatch: %s", filepath.Base(path))
	}
	return nil
}

func romFile(dir, name string) (string, error) {
	path, err := graphFile(dir, name)
	if err != nil {
		return "", err
	}
	romDir, err := filepath.Abs(filepath.Join(dir, "roms"))
	if err != nil {
		return "", err
	}
	if filepath.Dir(path) == romDir {
		info, err := os.Lstat(romDir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("roms must be a directory, not a symlink or file")
		}
		base := filepath.Base(path)
		if filepath.Ext(base) != ".rom" {
			return "", fmt.Errorf("invalid staged rom name")
		}
		hash := base[:len(base)-4]
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("invalid staged rom digest")
		}
		if err := checkROMDigest(path, hash); err != nil {
			return "", err
		}
	}
	return path, nil
}
