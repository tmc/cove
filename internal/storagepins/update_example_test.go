package storagepins_test

import (
	"fmt"
	"os"
	"time"

	"github.com/tmc/cove/internal/storagepins"
)

func ExampleUpdate() {
	root, _ := os.MkdirTemp("", "pin-update-example-")
	defer os.RemoveAll(root)
	err := storagepins.Update(root, func(pins *storagepins.File) (bool, error) { return true, pins.Add("vm", "workspace", time.Unix(0, 0)) })
	fmt.Println(err)
	pins, _ := storagepins.Load(root)
	fmt.Println(pins.List()[0].Ref())
	// Output:
	// <nil>
	// vm:workspace
}
