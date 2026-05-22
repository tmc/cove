package main

import (
	"errors"
	"fmt"

	"github.com/tmc/cove/internal/vmconfig"
)

// ErrMemoryExceedsConfigured is returned by validateMemoryTargetGB
// when the requested balloon target is larger than the VM's configured
// memory cap. Callers can branch on this with errors.Is to surface a
// "shrink or reconfigure" hint without parsing the message.
var ErrMemoryExceedsConfigured = errors.New("memory target exceeds configured memory")

const (
	bytesPerMiB = uint64(1024 * 1024)
	bytesPerGiB = uint64(1024 * 1024 * 1024)
)

func bytesToGB(size uint64) float64 {
	return float64(size) / float64(bytesPerGiB)
}

func configuredMemoryBytes(vmDirectory string) (uint64, error) {
	if vmDirectory != "" {
		cfg, err := vmconfig.Load(vmDirectory)
		if err != nil {
			return 0, fmt.Errorf("load vm config: %w", err)
		}
		if cfg.MemoryGB > 0 {
			return cfg.MemoryGB * bytesPerGiB, nil
		}
	}
	if memoryGB > 0 {
		return memoryGB * bytesPerGiB, nil
	}
	return 0, nil
}

func validateMemoryTargetGB(sizeGB float64, configuredBytes uint64) error {
	if configuredBytes == 0 {
		return nil
	}
	configuredGB := bytesToGB(configuredBytes)
	if sizeGB > configuredGB {
		return fmt.Errorf("%w: target %.2f GB > configured %.2f GB", ErrMemoryExceedsConfigured, sizeGB, configuredGB)
	}
	return nil
}
