package main

import (
	"errors"

	vz "github.com/tmc/apple/virtualization"
)

const nestedVirtualizationUnsupportedError = "nested virtualization requires M3/M4 chip on macOS 15+. Run without --nested to boot a standard VM (KVM will be disabled)."

func nestedVirtualizationSupported() bool {
	return vz.GetVZGenericPlatformConfigurationClass().IsNestedVirtualizationSupported()
}

func validateNestedVirtualizationSupported() error {
	if !nestedVirtualizationSupported() {
		return errors.New(nestedVirtualizationUnsupportedError)
	}
	return nil
}
