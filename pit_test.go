package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/apple/objectivec"
)

func TestPITSnapshotManagerSaveRestoreAndList(t *testing.T) {
	dir := t.TempDir()
	manager := NewPITSnapshotManager(dir)

	activeDisk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(activeDisk, []byte("live-disk"), 0644); err != nil {
		t.Fatalf("WriteFile(activeDisk) error = %v", err)
	}

	resumeCalled := false
	fixedNow := time.Date(2026, 4, 22, 11, 22, 33, 0, time.Local)
	err := manager.Save("checkpoint1", PITSaveHooks{
		Now: func() time.Time { return fixedNow },
		Pause: func() (bool, error) {
			return true, nil
		},
		Resume: func() error {
			resumeCalled = true
			return nil
		},
		SaveState: func(path string) error {
			return os.WriteFile(path, []byte("vmstate"), 0644)
		},
		CloneDisk: func(dst string) (int64, error) {
			if err := copyFile(activeDisk, dst); err != nil {
				return 0, err
			}
			return int64(len("live-disk")), nil
		},
		CurrentConfiguration: func() (objectivec.IObject, error) {
			return nil, fmt.Errorf("config unavailable")
		},
		Fingerprint: func() suspendConfigFingerprint {
			return suspendConfigFingerprint{
				CPUs:       4,
				MemoryGB:   8,
				Clipboard:  true,
				Serial:     true,
				USBDevices: 1,
			}
		},
		StateDescription: func() string {
			return "paused"
		},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !resumeCalled {
		t.Fatal("Save() did not call Resume hook")
	}

	info, err := manager.Load("checkpoint1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if info.Name != "checkpoint1" {
		t.Fatalf("Load() name = %q, want %q", info.Name, "checkpoint1")
	}
	if !info.Created.Equal(fixedNow) {
		t.Fatalf("Load() created = %v, want %v", info.Created, fixedNow)
	}
	if info.DiskFileName != "disk.img" {
		t.Fatalf("Load() disk file name = %q, want %q", info.DiskFileName, "disk.img")
	}
	if info.VMStateSize != int64(len("vmstate")) {
		t.Fatalf("Load() vm state size = %d, want %d", info.VMStateSize, len("vmstate"))
	}
	if info.DiskSize != int64(len("live-disk")) {
		t.Fatalf("Load() disk size = %d, want %d", info.DiskSize, len("live-disk"))
	}
	if !strings.Contains(info.FrameworkConfigError, "config unavailable") {
		t.Fatalf("Load() framework config error = %q", info.FrameworkConfigError)
	}
	if info.SuspendConfig == nil || info.SuspendConfig.CPUs != 4 || info.SuspendConfig.MemoryGB != 8 {
		t.Fatalf("Load() suspend config = %#v", info.SuspendConfig)
	}

	list, err := manager.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].Name != "checkpoint1" {
		t.Fatalf("List() = %#v", list)
	}

	if err := os.WriteFile(activeDisk, []byte("changed"), 0644); err != nil {
		t.Fatalf("WriteFile(activeDisk changed) error = %v", err)
	}
	if err := manager.Restore("checkpoint1"); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	gotDisk, err := os.ReadFile(activeDisk)
	if err != nil {
		t.Fatalf("ReadFile(activeDisk) error = %v", err)
	}
	if string(gotDisk) != "live-disk" {
		t.Fatalf("restored disk = %q, want %q", gotDisk, "live-disk")
	}

	gotState, err := os.ReadFile(filepath.Join(dir, "suspend.vmstate"))
	if err != nil {
		t.Fatalf("ReadFile(suspend.vmstate) error = %v", err)
	}
	if string(gotState) != "vmstate" {
		t.Fatalf("restored vm state = %q, want %q", gotState, "vmstate")
	}

	configData, err := os.ReadFile(filepath.Join(dir, "suspend.config.json"))
	if err != nil {
		t.Fatalf("ReadFile(suspend.config.json) error = %v", err)
	}
	var restored suspendConfigFingerprint
	if err := json.Unmarshal(configData, &restored); err != nil {
		t.Fatalf("Unmarshal(suspend.config.json) error = %v", err)
	}
	if restored.CPUs != 4 || restored.MemoryGB != 8 || !restored.Clipboard || !restored.Serial || restored.USBDevices != 1 {
		t.Fatalf("restored suspend config = %#v", restored)
	}
}

func TestParsePITActionRequest(t *testing.T) {
	req, err := parsePITActionRequest([]byte(`{"type":"pit","data":{"action":"swap","name":"clean-base","ram":true}}`))
	if err != nil {
		t.Fatalf("parsePITActionRequest() error = %v", err)
	}
	if req.Action != "swap" {
		t.Fatalf("action = %q, want %q", req.Action, "swap")
	}
	if req.Name != "clean-base" {
		t.Fatalf("name = %q, want %q", req.Name, "clean-base")
	}
	if !req.RAM {
		t.Fatal("ram = false, want true")
	}
}

func TestHandlePITJSONRequestNoVM(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handlePITJSONRequest([]byte(`{"action":"save","name":"checkpoint1"}`))
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.Error == "" {
		t.Fatalf("expected error, got %#v", resp)
	}
}

func TestPITSnapshotManagerDelete(t *testing.T) {
	dir := t.TempDir()
	manager := NewPITSnapshotManager(dir)

	// not found
	if err := manager.Delete("missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Delete(missing) error = %v, want not found", err)
	}

	// invalid name
	if err := manager.Delete("../escape"); err == nil {
		t.Fatal("Delete(invalid) error = nil, want validation error")
	}

	// success: pre-create the snapshot directory
	snapDir := manager.snapshotDir("alpha")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := manager.Delete("alpha"); err != nil {
		t.Fatalf("Delete(alpha) error = %v", err)
	}
	if _, err := os.Stat(snapDir); !os.IsNotExist(err) {
		t.Fatalf("snapshot dir still present after Delete: err = %v", err)
	}
}

func TestHandlePITSaveEarlyReturns(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())

	if resp := s.handlePITSave(""); resp == nil || !strings.Contains(resp.Error, "name required") {
		t.Fatalf("empty name resp = %#v", resp)
	}
	if resp := s.handlePITSave("checkpoint"); resp == nil || !strings.Contains(resp.Error, "VM not configured") {
		t.Fatalf("no-VM resp = %#v", resp)
	}
}

func TestHandlePITSwapEmptyName(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handlePITSwap("", false)
	if resp == nil || !strings.Contains(resp.Error, "name required") {
		t.Fatalf("empty name resp = %#v", resp)
	}
}

func TestHandlePITCommandDispatch(t *testing.T) {
	// empty args -> usage, no error
	if err := handlePITCommand(nil); err != nil {
		t.Fatalf("handlePITCommand(nil) error = %v", err)
	}
	// help -> no error
	if err := handlePITCommand([]string{"help"}); err != nil {
		t.Fatalf("handlePITCommand(help) error = %v", err)
	}
	// unknown command -> error
	err := handlePITCommand([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown pit command") {
		t.Fatalf("handlePITCommand(bogus) error = %v, want unknown", err)
	}
}
