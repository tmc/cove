package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check() error {
	path, err := syscall.UTF16PtrFromString(`\\.\PhysicalDrive0`)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(path, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return fmt.Errorf("open target: %w", err)
	}
	defer syscall.CloseHandle(h)
	var length uint64
	var returned uint32
	if err := syscall.DeviceIoControl(h, 0x7405c, nil, 0, (*byte)(unsafe.Pointer(&length)), 8, &returned, nil); err != nil {
		return fmt.Errorf("target length: %w", err)
	}
	if returned != 8 || length != 64<<30 {
		return fmt.Errorf("target length %d bytes, require dedicated 64 GiB disk", length)
	}
	data := make([]byte, 1024*1024)
	var n uint32
	if err := syscall.ReadFile(h, data, &n, nil); err != nil {
		return fmt.Errorf("read target partition area: %w", err)
	}
	if int(n) != len(data) || !unpartitioned(data) {
		return fmt.Errorf("target partition area contains data")
	}
	fmt.Printf("target PhysicalDrive0: %d bytes, MBR disk signature %x, no partition data in first MiB\n", length, data[440:444])
	return nil
}

func unpartitioned(data []byte) bool {
	if len(data) != 1024*1024 {
		return false
	}
	for i, b := range data {
		// WinPE assigns this signature before creating any partitions.
		if i >= 440 && i < 444 {
			continue
		}
		if b != 0 {
			return false
		}
	}
	return true
}
