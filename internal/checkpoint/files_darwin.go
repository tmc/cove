package checkpoint

import "golang.org/x/sys/unix"

func cloneFile(src, dst string) error { return unix.Clonefile(src, dst, 0) }
func publish(src, dst string) error   { return unix.RenamexNp(src, dst, unix.RENAME_EXCL) }
