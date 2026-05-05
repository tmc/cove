package softreset

import "golang.org/x/sys/unix"

func setXattr(path, name string, value []byte) error {
	return unix.Setxattr(path, name, value, 0)
}

func hasXattr(path, name string) bool {
	_, err := unix.Getxattr(path, name, nil)
	return err == nil
}
