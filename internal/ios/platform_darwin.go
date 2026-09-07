//go:build darwin

package ios

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/exp/research"
	"github.com/tmc/apple/x/vzkit/storage"
)

var identityFiles = [...]string{"hw.model", "machine.id", "aux.img"}

// openPlatform runs on the runtime's main thread inside an autorelease pool.
// The caller holds the bundle run lock and owns the returned reference.
// initialize permits first creation only; partial identity sets require repair.
func openPlatform(dir string, initialize bool) (vz.VZMacPlatformConfiguration, uint64, error) {
	present, err := identityPresent(dir)
	if err != nil {
		return vz.VZMacPlatformConfiguration{}, 0, err
	}
	if !present && !initialize {
		return vz.VZMacPlatformConfiguration{}, 0, fmt.Errorf("ios identity is not initialized")
	}
	model, err := newHardwareModel()
	if err != nil {
		return vz.VZMacPlatformConfiguration{}, 0, err
	}
	defer model.Release()
	if present {
		saved, err := os.ReadFile(filepath.Join(dir, "hw.model"))
		if err != nil {
			return vz.VZMacPlatformConfiguration{}, 0, fmt.Errorf("read ios hardware model: %w", err)
		}
		if !bytes.Equal(saved, storage.NSDataToBytes(model.DataRepresentation())) {
			return vz.VZMacPlatformConfiguration{}, 0, fmt.Errorf("saved ios hardware model does not match research profile")
		}
	}
	var machine vz.VZMacMachineIdentifier
	if present {
		machine, err = readMachineIdentifier(filepath.Join(dir, "machine.id"))
		if err != nil {
			return vz.VZMacPlatformConfiguration{}, 0, err
		}
	} else {
		machine = vz.NewVZMacMachineIdentifier()
		if machine.ID == 0 {
			return vz.VZMacPlatformConfiguration{}, 0, fmt.Errorf("create ios machine identifier: nil object")
		}
	}
	defer machine.Release()
	ecid, err := machineECID(machine)
	if err != nil {
		return vz.VZMacPlatformConfiguration{}, 0, err
	}
	if !present {
		if err := writeIdentityFile(filepath.Join(dir, "hw.model"), storage.NSDataToBytes(model.DataRepresentation())); err != nil {
			return vz.VZMacPlatformConfiguration{}, 0, err
		}
		if err := writeIdentityFile(filepath.Join(dir, "machine.id"), storage.NSDataToBytes(machine.DataRepresentation())); err != nil {
			return vz.VZMacPlatformConfiguration{}, 0, err
		}
	}
	url := foundation.NewURLFileURLWithPath(filepath.Join(dir, "aux.img"))
	if url.ID == 0 {
		return vz.VZMacPlatformConfiguration{}, 0, fmt.Errorf("create ios nvram url: nil object")
	}
	defer url.Release()
	var aux vz.VZMacAuxiliaryStorage
	if present {
		aux = vz.NewMacAuxiliaryStorageWithContentsOfURL(url)
	} else {
		aux, err = vz.NewMacAuxiliaryStorageCreatingStorageAtURLHardwareModelOptionsError(url, model, 0)
		if err != nil {
			return vz.VZMacPlatformConfiguration{}, 0, fmt.Errorf("create ios nvram: %w", err)
		}
	}
	if aux.ID == 0 {
		return vz.VZMacPlatformConfiguration{}, 0, fmt.Errorf("open ios nvram: nil object")
	}
	defer aux.Release()
	platform := vz.NewVZMacPlatformConfiguration()
	if platform.ID == 0 {
		return platform, 0, fmt.Errorf("create ios platform: nil object")
	}
	platform.SetHardwareModel(model)
	platform.SetMachineIdentifier(machine)
	platform.SetAuxiliaryStorage(aux)
	return platform, ecid, nil
}

func identityPresent(dir string) (bool, error) {
	present := 0
	for _, name := range identityFiles {
		info, err := os.Lstat(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("inspect ios %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return false, fmt.Errorf("ios %s must be a nonempty regular file", name)
		}
		present++
	}
	if present != 0 && present != len(identityFiles) {
		return false, fmt.Errorf("incomplete ios identity: preserve existing files and restore a consistent backup")
	}
	return present != 0, nil
}

func readMachineIdentifier(path string) (vz.VZMacMachineIdentifier, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return vz.VZMacMachineIdentifier{}, fmt.Errorf("read ios machine identifier: %w", err)
	}
	if len(data) == 0 {
		return vz.VZMacMachineIdentifier{}, fmt.Errorf("empty ios machine identifier")
	}
	nsData := foundation.NewDataWithBytesLength(data)
	if nsData.ID == 0 {
		return vz.VZMacMachineIdentifier{}, fmt.Errorf("decode ios machine identifier: nil data")
	}
	defer nsData.Release()
	machine := vz.NewMacMachineIdentifierWithDataRepresentation(nsData)
	if machine.ID == 0 {
		return machine, fmt.Errorf("invalid ios machine identifier")
	}
	return machine, nil
}

func machineECID(machine vz.VZMacMachineIdentifier) (uint64, error) {
	return research.ECID(machine)
}

func writeIdentityFile(path string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty ios identity data for %s", filepath.Base(path))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create ios identity file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write ios identity file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync ios identity file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close ios identity file: %w", err)
	}
	return nil
}
