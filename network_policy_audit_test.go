package main

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeNetworkAuditDir string

func (f fakeNetworkAuditDir) Dir() string { return string(f) }

func TestWriteActiveNetworkPolicyAuditNilRunNoOp(t *testing.T) {
	writeActiveNetworkPolicyAudit(nil)
}

func TestWriteActiveNetworkPolicyAuditOpenPolicySkips(t *testing.T) {
	prev := networkMode
	t.Cleanup(func() { networkMode = prev })
	networkMode = ""

	dir := fakeNetworkAuditDir(t.TempDir())
	writeActiveNetworkPolicyAudit(dir)
	if _, err := os.Stat(filepath.Join(string(dir), "network.log")); !os.IsNotExist(err) {
		t.Fatalf("network.log written for open policy: err = %v", err)
	}
}

func TestWriteActiveNetworkPolicyAuditOfflineWritesLog(t *testing.T) {
	prev := networkMode
	t.Cleanup(func() { networkMode = prev })
	networkMode = "offline"

	dir := fakeNetworkAuditDir(t.TempDir())
	writeActiveNetworkPolicyAudit(dir)
	if _, err := os.Stat(filepath.Join(string(dir), "network.log")); err != nil {
		t.Fatalf("network.log not written: %v", err)
	}
}
