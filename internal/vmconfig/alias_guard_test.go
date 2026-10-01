package vmconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/mutationguard"
)

func TestAliasMutationsRespectRootGuard(t *testing.T) {
	t.Setenv(StateDirEnv, t.TempDir())
	guard, err := mutationguard.Acquire(StateDir())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	dir := filepath.Join(BaseDir(), "vm.covevm")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		plain func() error
		held  func(*mutationguard.Guard) error
	}{
		{"registry", func() error { return EnsureAlias("vm", dir) }, func(g *mutationguard.Guard) error { return EnsureAliasWithGuard("vm", dir, g) }},
		{"compatibility", func() error { return EnsureCompatibilityAlias("vm", dir) }, func(g *mutationguard.Guard) error { return EnsureCompatibilityAliasWithGuard("vm", dir, g) }},
		{"finder", func() error { return EnsurePackageAlias("vm", dir) }, func(g *mutationguard.Guard) error { return EnsurePackageAliasWithGuard("vm", dir, g) }},
		{"finder list", func() error { return EnsurePackageAliases([]Info{{Name: "vm", Path: dir}}) }, func(g *mutationguard.Guard) error {
			return EnsurePackageAliasesWithGuard([]Info{{Name: "vm", Path: dir}}, g)
		}},
		{"remove compatibility", func() error { return RemoveCompatibilityAlias("vm") }, func(g *mutationguard.Guard) error { return RemoveCompatibilityAliasWithGuard("vm", g) }},
		{"remove finder", func() error { return RemovePackageAlias("vm") }, func(g *mutationguard.Guard) error { return RemovePackageAliasWithGuard("vm", g) }},
	}
	other, err := mutationguard.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.plain(); !errors.Is(err, mutationguard.ErrBusy) {
				t.Fatalf("plain writer bypassed held guard: %v", err)
			}
			if err := tt.held(nil); err == nil {
				t.Fatal("accepted nil guard")
			}
			if err := tt.held(other); err == nil {
				t.Fatal("accepted other root guard")
			}
			if err := tt.held(guard); err != nil {
				t.Fatalf("held guard failed: %v", err)
			}
		})
	}
	guard.Release()
	for _, tt := range tests {
		t.Run(tt.name+" released", func(t *testing.T) {
			if err := tt.held(guard); err == nil {
				t.Fatal("accepted released guard")
			}
			if err := tt.plain(); err != nil {
				t.Fatalf("plain writer failed after release: %v", err)
			}
		})
	}
}
