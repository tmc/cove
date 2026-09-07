//go:build darwin

package ios

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/ios/bundle"
	"github.com/tmc/cove/internal/vmconfig"
)

// CreateBundle creates blank disk and SEP storage with the supplied configuration.
// dir must not exist. CPU and memory must fit the current VZ host limits.
// It does not create firmware, machine identity, or NVRAM.
func CreateBundle(dir string, config bundle.Config, cpu uint, memoryGB uint64, diskBytes int64) (err error) {
	if err := config.Validate(); err != nil {
		return err
	}
	if cpu == 0 || memoryGB == 0 || memoryGB > ^uint64(0)/(1<<30) || diskBytes <= 0 || diskBytes%512 != 0 {
		return fmt.Errorf("invalid ios cpu, memory or disk size")
	}
	if err := validateHardware(cpu, memoryGB); err != nil {
		return err
	}
	if config.FirmwareDigest != "" || config.ROM != "" || config.SEPROM != "" {
		return fmt.Errorf("blank ios bundle must not reference firmware")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("create ios bundle: %w", err)
	}
	defer func() {
		if err != nil {
			if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
				err = fmt.Errorf("%w; remove incomplete bundle: %v", err, cleanupErr)
			}
		}
	}()
	for _, file := range []struct {
		name string
		size int64
	}{
		{"disk.img", diskBytes}, {"sep.img", 512 << 10},
	} {
		f, err := os.OpenFile(filepath.Join(dir, file.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("create %s: %w", file.name, err)
		}
		truncateErr := f.Truncate(file.size)
		closeErr := f.Close()
		if truncateErr != nil {
			return fmt.Errorf("size %s: %w", file.name, truncateErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", file.name, closeErr)
		}
	}
	// Publish the guest type only after its blank storage is complete.
	return vmconfig.Save(dir, &vmconfig.Config{CPU: cpu, MemoryGB: memoryGB, IOS: &config})
}
