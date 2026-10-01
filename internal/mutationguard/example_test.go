package mutationguard_test

import (
	"context"
	"fmt"
	"os"

	"github.com/tmc/cove/internal/mutationguard"
)

func ExampleAcquire() {
	root, _ := os.MkdirTemp("", "mutation-guard-example-")
	defer os.RemoveAll(root)
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer guard.Release()
	fmt.Println("guard acquired")
	// Output: guard acquired
}

func ExampleGuard_Release() {
	var guard mutationguard.Guard
	fmt.Println(guard.Release())
	// Output: <nil>
}

func ExampleGuard_Check() {
	root, _ := os.MkdirTemp("", "mutation-guard-example-")
	defer os.RemoveAll(root)
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer guard.Release()
	fmt.Println(guard.Check(root))
	// Output: <nil>
}

func ExampleAcquireContext() {
	root, _ := os.MkdirTemp("", "mutation-guard-example-")
	defer os.RemoveAll(root)
	guard, err := mutationguard.AcquireContext(context.Background(), root)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer guard.Release()
	fmt.Println("guard acquired")
	// Output: guard acquired
}
