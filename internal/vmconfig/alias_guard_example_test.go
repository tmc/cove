package vmconfig_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/vmconfig"
)

func exampleAliasGuard() (*mutationguard.Guard, string, func()) {
	root, restore := exampleMutationRoot()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		restore()
		return nil, "", func() {}
	}
	dir, _ := vmconfig.EnsureDirWithGuard("workspace", "", guard)
	os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0600)
	return guard, dir, func() { guard.Release(); restore() }
}

func ExampleEnsureAliasWithGuard() {
	guard, dir, close := exampleAliasGuard()
	defer close()
	fmt.Println(vmconfig.EnsureAliasWithGuard("external", dir, guard))
	// Output: <nil>
}
func ExampleEnsureCompatibilityAliasWithGuard() {
	guard, dir, close := exampleAliasGuard()
	defer close()
	fmt.Println(vmconfig.EnsureCompatibilityAliasWithGuard("workspace", dir, guard))
	// Output: <nil>
}
func ExampleEnsurePackageAliasWithGuard() {
	guard, dir, close := exampleAliasGuard()
	defer close()
	fmt.Println(vmconfig.EnsurePackageAliasWithGuard("workspace", dir, guard))
	// Output: <nil>
}
func ExampleEnsurePackageAliasesWithGuard() {
	guard, dir, close := exampleAliasGuard()
	defer close()
	fmt.Println(vmconfig.EnsurePackageAliasesWithGuard([]vmconfig.Info{{Name: "workspace", Path: dir}}, guard))
	// Output: <nil>
}
func ExampleRemoveCompatibilityAliasWithGuard() {
	guard, _, close := exampleAliasGuard()
	defer close()
	fmt.Println(vmconfig.RemoveCompatibilityAliasWithGuard("workspace", guard))
	// Output: <nil>
}
func ExampleRemovePackageAliasWithGuard() {
	guard, dir, close := exampleAliasGuard()
	defer close()
	vmconfig.EnsurePackageAliasWithGuard("workspace", dir, guard)
	fmt.Println(vmconfig.RemovePackageAliasWithGuard("workspace", guard))
	// Output: <nil>
}
