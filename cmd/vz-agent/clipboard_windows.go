package main

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	user32                = syscall.NewLazyDLL("user32.dll")
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard     = user32.NewProc("OpenClipboard")
	procCloseClipboard    = user32.NewProc("CloseClipboard")
	procEmptyClipboard    = user32.NewProc("EmptyClipboard")
	procGetClipboardData  = user32.NewProc("GetClipboardData")
	procSetClipboardData  = user32.NewProc("SetClipboardData")
	procIsClipboardFormat = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalAlloc       = kernel32.NewProc("GlobalAlloc")
	procGlobalLock        = kernel32.NewProc("GlobalLock")
	procGlobalUnlock      = kernel32.NewProc("GlobalUnlock")
	procGlobalFree        = kernel32.NewProc("GlobalFree")
	procGlobalSize        = kernel32.NewProc("GlobalSize")
)

func clipboardGetText() (string, error) {
	if ok, _, _ := procIsClipboardFormat.Call(cfUnicodeText); ok == 0 {
		return "", nil
	}
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", fmt.Errorf("get clipboard data failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("lock clipboard data failed")
	}
	defer procGlobalUnlock.Call(h)

	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return "", nil
	}
	chars := int(size) / 2
	data := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), chars)
	for i, c := range data {
		if c == 0 {
			data = data[:i]
			break
		}
	}
	return syscall.UTF16ToString(data), nil
}

func clipboardSetText(text string) error {
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	size := uintptr(len(utf16) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("allocate clipboard memory failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("lock clipboard memory failed")
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(utf16)), utf16)
	procGlobalUnlock.Call(h)

	if err := openClipboard(); err != nil {
		procGlobalFree.Call(h)
		return err
	}
	defer procCloseClipboard.Call()
	if ok, _, _ := procEmptyClipboard.Call(); ok == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("empty clipboard failed")
	}
	if ok, _, _ := procSetClipboardData.Call(cfUnicodeText, h); ok == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("set clipboard data failed")
	}
	return nil
}

func openClipboard() error {
	for i := 0; i < 20; i++ {
		if ok, _, _ := procOpenClipboard.Call(0); ok != 0 {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("open clipboard failed")
}
