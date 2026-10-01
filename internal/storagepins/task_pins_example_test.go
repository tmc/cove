package storagepins_test

import (
	"fmt"
	"os"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
)

func exampleTaskPin() storagepins.TaskPin {
	return storagepins.TaskPin{Category: "vm", ID: "workspace", AddedAt: time.Unix(100, 0), Owner: storagepins.TaskOwner{RunID: "run", AttemptID: "attempt", Generation: "0123456789abcdef0123456789abcdef", PID: 123, StartedAt: "2026-10-01T12:00:00Z"}, Identity: storagepins.DirectoryIdentity{Path: "/guest/workspace", Device: 1, Inode: 2}}
}

func ExampleFile_AddTask() {
	pins := storagepins.New()
	fmt.Println(pins.AddTask(exampleTaskPin()))
	fmt.Println(pins.IsPinned("vm", "workspace"))
	// Output:
	// <nil>
	// true
}
func ExampleFile_RemoveTask() {
	pins := storagepins.New()
	pin := exampleTaskPin()
	pins.AddTask(pin)
	removed, err := pins.RemoveTask(pin.Category, pin.ID, pin.Owner, pin.Identity)
	fmt.Println(removed, err)
	// Output: true <nil>
}
func ExampleFile_TaskPins() {
	pins := storagepins.New()
	pins.AddTask(exampleTaskPin())
	fmt.Println(pins.TaskPins()[0].Ref())
	// Output: vm:workspace
}
func ExampleTaskPin_Ref() {
	fmt.Println(exampleTaskPin().Ref())
	// Output: vm:workspace
}
func ExampleFile_IsOperatorPinned() {
	pins := storagepins.New()
	pins.AddTask(exampleTaskPin())
	fmt.Println(pins.IsOperatorPinned("vm", "workspace"), pins.IsPinned("vm", "workspace"))
	// Output: false true
}
func ExampleUpdateWithGuard() {
	root, _ := os.MkdirTemp("", "task-pin-example-")
	defer os.RemoveAll(root)
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer guard.Release()
	err = storagepins.UpdateWithGuard(root, guard, func(pins *storagepins.File) (bool, error) { return true, pins.AddTask(exampleTaskPin()) })
	fmt.Println(err)
	// Output: <nil>
}
