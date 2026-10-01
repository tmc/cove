package vmconfig_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/vmconfig"
)

func exampleMutationRoot() (string, func()) {
	root, _ := os.MkdirTemp("", "vmconfig-example-")
	previous, present := os.LookupEnv(vmconfig.StateDirEnv)
	os.Setenv(vmconfig.StateDirEnv, root)
	return root, func() {
		if present {
			os.Setenv(vmconfig.StateDirEnv, previous)
		} else {
			os.Unsetenv(vmconfig.StateDirEnv)
		}
		os.RemoveAll(root)
	}
}

func ExampleEnsureDirWithGuard() {
	root, restore := exampleMutationRoot()
	defer restore()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer guard.Release()
	dir, err := vmconfig.EnsureDirWithGuard("workspace", "", guard)
	fmt.Println(filepath.Base(dir), err)
	// Output: workspace.covevm <nil>
}

func ExampleMigrateIfNeededWithGuard() {
	root, restore := exampleMutationRoot()
	defer restore()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer guard.Release()
	fmt.Println(vmconfig.MigrateIfNeededWithGuard(guard))
	// Output: <nil>
}

func ExampleEnsurePackageLayoutWithGuard() {
	root, restore := exampleMutationRoot()
	defer restore()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer guard.Release()
	dir, _ := vmconfig.EnsureDirWithGuard("workspace", "", guard)
	packaged, err := vmconfig.EnsurePackageLayoutWithGuard("workspace", dir, guard)
	fmt.Println(filepath.Base(packaged), err)
	// Output: workspace.covevm <nil>
}

func ExampleListReadOnly() {
	_, restore := exampleMutationRoot()
	defer restore()
	infos, err := vmconfig.ListReadOnly(nil)
	fmt.Println(len(infos), err)
	// Output: 0 <nil>
}
