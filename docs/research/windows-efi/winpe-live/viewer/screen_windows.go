package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var user = syscall.NewLazyDLL("user32.dll")
var gdi = syscall.NewLazyDLL("gdi32.dll")

func main() {
	runtime.LockOSThread()
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	out := os.Args[1]
	log, err := os.Create(filepath.Join(out, "SCREENLOG.TXT"))
	if err != nil {
		os.Exit(1)
	}
	defer log.Close()
	fmt.Fprintln(log, "guest GDI capture started")
	log.Sync()
	time.Sleep(5 * time.Second)
	hwnd := editWindow()
	if hwnd == 0 {
		fmt.Fprintln(log, "edit window creation failed")
		return
	}
	time.Sleep(time.Second)
	for i := 0; i < 6; i++ {
		path := filepath.Join(out, fmt.Sprintf("SCREEN%d.PNG", i))
		err := capture(path)
		if err == nil {
			err = upload(out, path)
		}
		fmt.Fprintf(log, "capture %d: %v; foreground=%q\n", i, err, foreground())
		fmt.Fprintf(log, "edit text=%q\n", windowText(hwnd))
		log.Sync()
		time.Sleep(2 * time.Second)
	}
}

func capture(path string) error {
	w, _, _ := user.NewProc("GetSystemMetrics").Call(0)
	h, _, _ := user.NewProc("GetSystemMetrics").Call(1)
	if w == 0 || h == 0 || w > 8192 || h > 8192 {
		return fmt.Errorf("invalid screen dimensions %dx%d", w, h)
	}
	dc, _, err := user.NewProc("GetDC").Call(0)
	if dc == 0 {
		return fmt.Errorf("get screen dc: %v", err)
	}
	defer user.NewProc("ReleaseDC").Call(0, dc)
	mem, _, err := gdi.NewProc("CreateCompatibleDC").Call(dc)
	if mem == 0 {
		return fmt.Errorf("create memory dc: %v", err)
	}
	defer gdi.NewProc("DeleteDC").Call(mem)
	bitmap, _, err := gdi.NewProc("CreateCompatibleBitmap").Call(dc, w, h)
	if bitmap == 0 {
		return fmt.Errorf("create bitmap: %v", err)
	}
	defer gdi.NewProc("DeleteObject").Call(bitmap)
	old, _, err := gdi.NewProc("SelectObject").Call(mem, bitmap)
	if old == 0 || old == ^uintptr(0) {
		return fmt.Errorf("select bitmap: %v", err)
	}
	ok, _, err := gdi.NewProc("BitBlt").Call(mem, 0, 0, w, h, dc, 0, 0, 0x00cc0020)
	gdi.NewProc("SelectObject").Call(mem, old)
	if ok == 0 {
		return fmt.Errorf("copy screen: %v", err)
	}
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		ClrUsed, ClrImportant  uint32
	}{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	pixels := make([]byte, int(w*h*4))
	rows, _, err := gdi.NewProc("GetDIBits").Call(dc, bitmap, 0, h, uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&header)), 0)
	runtime.KeepAlive(pixels)
	if rows != h {
		return fmt.Errorf("read bitmap: rows %d of %d: %v", rows, h, err)
	}
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
		pixels[i+3] = 255
	}
	img := &image.RGBA{Pix: pixels, Stride: int(w) * 4, Rect: image.Rect(0, 0, int(w), int(h))}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err = png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func upload(out, path string) error {
	endpoint, err := os.ReadFile(filepath.Join(out, "PUSH.URL"))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Post(strings.TrimSpace(string(endpoint)), "image/png", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("upload status %s", response.Status)
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return err
	}
	switch strings.TrimSpace(string(reply)) {
	case "":
		return nil
	default:
		if strings.HasPrefix(string(reply), "text:") {
			return typeText(strings.TrimPrefix(string(reply), "text:"))
		}
		return fmt.Errorf("unknown input request")
	}
}

func typeText(text string) error {
	type input struct {
		Type, Pad   uint32
		Key, Scan   uint16
		Flags, Time uint32
		Extra       uintptr
		Tail        [8]byte
	}
	var events []input
	for _, c := range utf16.Encode([]rune(text)) {
		events = append(events, input{Type: 1, Scan: uint16(c), Flags: 4}, input{Type: 1, Scan: uint16(c), Flags: 6})
	}
	if len(events) == 0 {
		return nil
	}
	count, _, err := user.NewProc("SendInput").Call(uintptr(len(events)), uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0]))
	runtime.KeepAlive(events)
	if count != uintptr(len(events)) {
		return fmt.Errorf("send input: %d of %d: %v", count, len(events), err)
	}
	return nil
}

func foreground() string {
	hwnd, _, _ := user.NewProc("GetForegroundWindow").Call()
	var text [256]uint16
	user.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	return syscall.UTF16ToString(text[:])
}

func windowText(hwnd uintptr) string {
	var text [256]uint16
	user.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	return syscall.UTF16ToString(text[:])
}

func editWindow() uintptr {
	ready := make(chan uintptr, 1)
	go func() {
		runtime.LockOSThread()
		class, _ := syscall.UTF16PtrFromString("EDIT")
		title, _ := syscall.UTF16PtrFromString("INPUT: ")
		hwnd, _, _ := user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0x10cf0004, 100, 100, 800, 350, 0, 0, 0, 0)
		if hwnd == 0 {
			ready <- 0
			return
		}
		user.NewProc("ShowWindow").Call(hwnd, 5)
		user.NewProc("UpdateWindow").Call(hwnd)
		user.NewProc("SetForegroundWindow").Call(hwnd)
		user.NewProc("SetFocus").Call(hwnd)
		ready <- hwnd
		var message struct {
			Window  uintptr
			ID      uint32
			WParam  uintptr
			LParam  uintptr
			Time    uint32
			X, Y    int32
			Private uint32
		}
		for {
			r, _, _ := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if r == 0 || int32(r) == -1 {
				return
			}
			user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
			user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	return <-ready
}
