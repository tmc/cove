//go:build !darwin

package checkpoint

import (
	"fmt"
	"os"
)

func cloneFile(src, dst string) error { return fmt.Errorf("clone unsupported") }
func publish(src, dst string) error {
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		return fmt.Errorf("checkpoint destination exists or cannot be inspected")
	}
	return os.Rename(src, dst)
}
