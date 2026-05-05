//go:build !darwin && !linux

package softreset

import "fmt"

func setXattr(path, name string, value []byte) error {
	return fmt.Errorf("xattr unsupported")
}

func hasXattr(path, name string) bool {
	return false
}
