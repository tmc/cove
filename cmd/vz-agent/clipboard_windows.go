package main

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	clipboardMu           sync.Mutex
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
	procCreateWindowEx    = user32.NewProc("CreateWindowExW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
)

func clipboardGetText() (string, error) {
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(0); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()
	if ok, _, _ := procIsClipboardFormat.Call(cfUnicodeText); ok == 0 {
		return "", nil
	}

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
		return fmt.Errorf("encode clipboard text: %w", err)
	}
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// EmptyClipboard requires a window owner for SetClipboardData to succeed.
	class := [...]uint16{'S', 'T', 'A', 'T', 'I', 'C', 0}
	hwnd, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(&class[0])), 0, 0,
		0, 0, 0, 0, ^uintptr(2), 0, 0, 0) // HWND_MESSAGE = -3
	if hwnd == 0 {
		return fmt.Errorf("create clipboard window: %w", err)
	}
	defer procDestroyWindow.Call(hwnd)
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

	if err := openClipboard(hwnd); err != nil {
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

func openClipboard(hwnd uintptr) error {
	var err error
	for i := 0; i < 20; i++ {
		var ok uintptr
		ok, _, err = procOpenClipboard.Call(hwnd)
		if ok != 0 {
			return nil
		}
		if i < 19 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return fmt.Errorf("open clipboard: %w", err)
}
