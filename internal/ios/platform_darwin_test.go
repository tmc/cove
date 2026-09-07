package ios

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/storage"
)

func TestIdentityPresent(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			dir := t.TempDir()
			for i, name := range identityFiles {
				if mask&(1<<i) != 0 {
					if err := os.WriteFile(filepath.Join(dir, name), []byte("existing state"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			present, err := identityPresent(dir)
			if mask == 0 || mask == 7 {
				if err != nil || present != (mask == 7) {
					t.Fatalf("present=%v err=%v", present, err)
				}
			} else if err == nil {
				t.Fatal("partial identity accepted")
			}
		})
	}
	for _, kind := range []string{"empty", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "hw.model")
			var err error
			switch kind {
			case "empty":
				err = os.WriteFile(path, nil, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink("missing", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := identityPresent(dir); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}

func TestMachineIdentityPersistence(t *testing.T) {
	objc.AutoreleasePool(func() {
		machine := vz.NewVZMacMachineIdentifier()
		if machine.ID == 0 {
			t.Fatal("nil identifier")
		}
		defer machine.Release()
		data := storage.NSDataToBytes(machine.DataRepresentation())
		path := filepath.Join(t.TempDir(), "machine.id")
		if err := writeIdentityFile(path, data); err != nil {
			t.Fatal(err)
		}
		loaded, err := readMachineIdentifier(path)
		if err != nil {
			t.Fatal(err)
		}
		defer loaded.Release()
		if !bytes.Equal(storage.NSDataToBytes(loaded.DataRepresentation()), data) {
			t.Fatal("identity changed on reload")
		}
		ecid, err := machineECID(machine)
		if err != nil {
			t.Fatal(err)
		}
		reloadedECID, err := machineECID(loaded)
		if err != nil {
			t.Fatal(err)
		}
		if reloadedECID != ecid {
			t.Fatal("ecid changed on reload")
		}
		if err := writeIdentityFile(path, []byte("replacement")); err == nil {
			t.Fatal("overwrote identity")
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, data) {
			t.Fatal("collision changed identity")
		}
		t.Log("machine identifier and nonzero ECID survive save/reload; no VM created")
	})
}

func TestCorruptMachineIdentityPreserved(t *testing.T) {
	objc.AutoreleasePool(func() {
		path := filepath.Join(t.TempDir(), "machine.id")
		data := []byte("not a machine identifier")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		loaded, err := readMachineIdentifier(path)
		if loaded.ID != 0 {
			loaded.Release()
		}
		if err == nil {
			t.Fatal("corrupt identity accepted")
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, data) {
			t.Fatal("corrupt identity was overwritten")
		}
	})
}

func TestPlatformPropertyOwnership(t *testing.T) {
	class := objc.GetClass("VZMacPlatformConfiguration")
	for _, name := range []string{"hardwareModel", "machineIdentifier", "auxiliaryStorage"} {
		property := objectivec.Class_getProperty(class, name)
		if property == nil {
			t.Fatalf("missing property %s", name)
		}
		attributes := objc.GoString(objectivec.Property_getAttributes(property))
		t.Logf("%s: %s", name, attributes)
		if !strings.Contains(attributes, ",&") && !strings.Contains(attributes, ",C") {
			t.Errorf("%s does not retain or copy its value", name)
		}
	}
}
