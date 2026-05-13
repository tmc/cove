//go:build darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	privvz "github.com/tmc/apple/private/virtualization"
)

// TestCloneMachineIdentifier exercises the private _machineIdentifierForVirtualMachineClone API.
func TestCloneMachineIdentifier(t *testing.T) {
	cls := privvz.GetVZMacMachineIdentifierClass()
	if cls.Class() == 0 {
		t.Fatal("VZMacMachineIdentifier class is nil")
	}

	result, err := cls.MachineIdentifierForVirtualMachineClone()
	if err != nil {
		t.Skipf("private clone identifier API unavailable: %v", err)
	}
	if result == nil || result.GetID() == 0 {
		t.Fatal("MachineIdentifierForVirtualMachineClone returned nil")
	}

	className := foundation.NSStringFromID(objc.Send[objc.ID](result.GetID(), objc.Sel("className"))).String()
	t.Logf("clone identifier class: %s", className)
	if className != "VZMacMachineIdentifier" {
		t.Errorf("expected VZMacMachineIdentifier, got %s", className)
	}

	ecid, ok := cloneMachineIdentifierECID(result.GetID())
	if !ok {
		t.Skip("_ECID unavailable")
	}
	t.Logf("clone ECID: %d", ecid)
	if ecid == 0 {
		t.Error("clone identifier has zero ECID")
	}
}

// TestCloneMachineIdentifierUniqueness verifies that each clone gets a unique ECID.
func TestCloneMachineIdentifierUniqueness(t *testing.T) {
	cls := privvz.GetVZMacMachineIdentifierClass()

	const n = 5
	ecids := make(map[uint64]bool, n)
	for i := 0; i < n; i++ {
		result, err := cls.MachineIdentifierForVirtualMachineClone()
		if err != nil {
			t.Skipf("private clone identifier API unavailable: %v", err)
		}
		if result == nil || result.GetID() == 0 {
			t.Fatalf("iteration %d: MachineIdentifierForVirtualMachineClone returned nil", i)
		}
		ecid, ok := cloneMachineIdentifierECID(result.GetID())
		if !ok {
			t.Skip("_ECID unavailable")
		}
		if ecids[ecid] {
			t.Errorf("duplicate ECID %d at iteration %d", ecid, i)
		}
		ecids[ecid] = true
	}
	t.Logf("generated %d unique ECIDs", len(ecids))
}

// TestCloneMachineIdentifierWithSerialNumber exercises
// _machineIdentifierForVirtualMachineCloneWithSerialNumber.
func TestCloneMachineIdentifierWithSerialNumber(t *testing.T) {
	cls := privvz.GetVZMacMachineIdentifierClass()

	serial := foundation.NSStringFromID(objc.String("TEST-SERIAL-001"))
	result, err := cls.MachineIdentifierForVirtualMachineCloneWithSerialNumber(serial)
	if err != nil {
		t.Skipf("private clone identifier API unavailable: %v", err)
	}
	if result == nil || result.GetID() == 0 {
		t.Fatal("MachineIdentifierForVirtualMachineCloneWithSerialNumber returned nil")
	}

	ecid, ok := cloneMachineIdentifierECID(result.GetID())
	if !ok {
		t.Skip("_ECID unavailable")
	}
	t.Logf("clone with serial: ECID=%d", ecid)
	if ecid == 0 {
		t.Error("clone with serial has zero ECID")
	}
}

// TestCloneMachineIdentifierWithECIDAndSerial exercises
// _machineIdentifierForVirtualMachineCloneWithECID:serialNumber:.
func TestCloneMachineIdentifierWithECIDAndSerial(t *testing.T) {
	cls := privvz.GetVZMacMachineIdentifierClass()

	var customECID uint64 = 0xDEADBEEF
	serial := foundation.NSStringFromID(objc.String("TEST-SERIAL-002"))
	result, err := cls.MachineIdentifierForVirtualMachineCloneWithECIDSerialNumber(customECID, serial)
	if err != nil {
		t.Skipf("private clone identifier API unavailable: %v", err)
	}
	if result == nil || result.GetID() == 0 {
		t.Fatal("MachineIdentifierForVirtualMachineCloneWithECIDSerialNumber returned nil")
	}

	gotECID, ok := cloneMachineIdentifierECID(result.GetID())
	if !ok {
		t.Skip("_ECID unavailable")
	}
	t.Logf("clone with custom ECID: requested=%d got=%d", customECID, gotECID)
	if gotECID != customECID {
		t.Errorf("ECID mismatch: got %d, want %d", gotECID, customECID)
	}
}

func cloneMachineIdentifierECID(id objc.ID) (uint64, bool) {
	if !objc.RespondsToSelector(id, objc.Sel("_ECID")) {
		return 0, false
	}
	return objc.Send[uint64](id, objc.Sel("_ECID")), true
}

// TestGenerateMachineID verifies that generateMachineID creates a valid machine.id file.
func TestGenerateMachineID(t *testing.T) {
	tmpDir := t.TempDir()
	if err := generateMachineID(tmpDir); err != nil {
		t.Fatalf("generateMachineID: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "machine.id"))
	if err != nil {
		t.Fatalf("read machine.id: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("machine.id is empty")
	}
	t.Logf("machine.id: %d bytes", len(data))

	// Verify round-trip: the data should create a valid VZMacMachineIdentifier.
	nsData := foundation.NewDataWithBytesLength(data)
	machineID := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacMachineIdentifier")), objc.Sel("alloc")),
		objc.Sel("initWithDataRepresentation:"), nsData.ID,
	)
	if machineID == 0 {
		t.Fatal("could not create VZMacMachineIdentifier from generated data")
	}
	t.Logf("round-trip class: %s",
		foundation.NSStringFromID(objc.Send[objc.ID](machineID, objc.Sel("className"))).String())
}

// TestGenerateMachineIDUniqueness verifies that successive calls produce different IDs.
func TestGenerateMachineIDUniqueness(t *testing.T) {
	dirs := make([]string, 3)
	for i := range dirs {
		dirs[i] = t.TempDir()
		if err := generateMachineID(dirs[i]); err != nil {
			t.Fatalf("generateMachineID[%d]: %v", i, err)
		}
	}

	datas := make([][]byte, len(dirs))
	for i, d := range dirs {
		var err error
		datas[i], err = os.ReadFile(filepath.Join(d, "machine.id"))
		if err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < len(datas); i++ {
		for j := i + 1; j < len(datas); j++ {
			if string(datas[i]) == string(datas[j]) {
				t.Errorf("machine.id[%d] == machine.id[%d] (should be unique)", i, j)
			}
		}
	}
}
