package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var user = syscall.NewLazyDLL("user32.dll")
var gdi = syscall.NewLazyDLL("gdi32.dll")
var session = time.Now().UTC().Format("20060102T150405") + "-" + strconv.Itoa(os.Getpid())

func main() {
	runtime.LockOSThread()
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	out := os.Args[1]
	log, err := os.Create(filepath.Join(out, "screen-"+time.Now().UTC().Format("20060102T150405")+".log"))
	if err != nil {
		os.Exit(1)
	}
	defer log.Close()
	fmt.Fprintf(log, "guest GDI capture started systemroot=%s user=%s arch=%s\n", os.Getenv("SYSTEMROOT"), os.Getenv("USERNAME"), runtime.GOARCH)
	log.Sync()
	commands := make(chan string, 32)
	go pollInput(out, log, commands)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()
	for i := 0; ; {
		select {
		case <-deadline.C:
			return
		case command := <-commands:
			result := applyInput(command)
			fmt.Fprintf(log, "input %q: %v; foreground=%q\n", command, result, foreground())
			log.Sync()
		case <-ticker.C:
			path := filepath.Join(out, "latest.png")
			err := capture(path)
			if err == nil {
				err = upload(out, path)
			}
			fmt.Fprintf(log, "capture %d: %v; foreground=%q\n", i, err, foreground())
			log.Sync()
			i++
		}
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
	request, err := http.NewRequest(http.MethodPost, strings.TrimSpace(string(endpoint)), bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "image/png")
	request.Header.Set("X-Control-Poll", "1")
	request.Header.Set("X-Systemroot", os.Getenv("SYSTEMROOT"))
	request.Header.Set("X-Session", session)
	uptime, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetTickCount64").Call()
	request.Header.Set("X-Uptime", strconv.FormatUint(uint64(uptime), 10))
	response, err := client.Do(request)
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
	return applyInput(string(reply))
}

func applyInput(command string) error {
	reply := []byte(command)
	switch strings.TrimSpace(command) {
	case "":
		return nil
	default:
		if strings.HasPrefix(string(reply), "text:") {
			return typeText(strings.TrimPrefix(string(reply), "text:"))
		}
		if strings.HasPrefix(string(reply), "key:") {
			key, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(string(reply), "key:")), 0, 16)
			if err != nil {
				return err
			}
			return pressKey(uint16(key))
		}
		if strings.HasPrefix(string(reply), "click:") {
			var x, y int
			if _, err := fmt.Sscanf(string(reply), "click:%d,%d", &x, &y); err != nil {
				return err
			}
			return click(x, y)
		}
		if strings.HasPrefix(command, "kbd:") {
			var key uint16
			var down int
			if _, err := fmt.Sscanf(command, "kbd:%d,%d", &key, &down); err != nil {
				return err
			}
			if down != 0 && down != 1 {
				return fmt.Errorf("invalid key state")
			}
			return keyEvent(key, down == 1)
		}
		if strings.HasPrefix(command, "pointer:") {
			var x, y int
			var flags uint32
			if _, err := fmt.Sscanf(command, "pointer:%d,%d,%d", &x, &y, &flags); err != nil {
				return err
			}
			if flags & ^uint32(0x1f) != 0 {
				return fmt.Errorf("invalid mouse flags")
			}
			return pointer(x, y, flags)
		}
		if strings.HasPrefix(command, "wheel:") {
			value, err := strconv.ParseInt(strings.TrimPrefix(command, "wheel:"), 10, 32)
			if err != nil {
				return err
			}
			return mouseEvent(0, 0, uint32(int32(value)), 0x800)
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

func pressKey(key uint16) error {
	type input struct {
		Type, Pad   uint32
		Key, Scan   uint16
		Flags, Time uint32
		Extra       uintptr
		Tail        [8]byte
	}
	events := []input{{Type: 1, Key: key}, {Type: 1, Key: key, Flags: 2}}
	n, _, err := user.NewProc("SendInput").Call(2, uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0]))
	runtime.KeepAlive(events)
	if n != 2 {
		return fmt.Errorf("press key: %d of 2: %v", n, err)
	}
	return nil
}

func click(x, y int) error {
	w, _, _ := user.NewProc("GetSystemMetrics").Call(0)
	h, _, _ := user.NewProc("GetSystemMetrics").Call(1)
	if x < 0 || y < 0 || x >= int(w) || y >= int(h) || w < 2 || h < 2 {
		return fmt.Errorf("click outside screen")
	}
	type input struct {
		Type, Pad         uint32
		X, Y              int32
		Data, Flags, Time uint32
		Extra             uintptr
	}
	events := []input{
		{X: int32(x * 65535 / int(w-1)), Y: int32(y * 65535 / int(h-1)), Flags: 0x8001},
		{Flags: 2}, {Flags: 4},
	}
	n, _, err := user.NewProc("SendInput").Call(3, uintptr(unsafe.Pointer(&events[0])), unsafe.Sizeof(events[0]))
	runtime.KeepAlive(events)
	if n != 3 {
		return fmt.Errorf("click: %d of 3: %v", n, err)
	}
	return nil
}

func pollInput(out string, log *os.File, inputs chan<- string) {
	endpoint, err := os.ReadFile(filepath.Join(out, "PUSH.URL"))
	if err != nil {
		fmt.Fprintln(log, err)
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	for end := time.Now().Add(30 * time.Minute); time.Now().Before(end); {
		request, err := http.NewRequest(http.MethodGet, strings.TrimSpace(string(endpoint))+"/control", nil)
		if err != nil {
			fmt.Fprintln(log, err)
			return
		}
		request.Header.Set("X-Session", session)
		response, err := client.Do(request)
		if err == nil {
			var commands []string
			if response.StatusCode != 200 {
				err = fmt.Errorf("control status %s", response.Status)
			} else {
				err = json.NewDecoder(io.LimitReader(response.Body, 32768)).Decode(&commands)
			}
			response.Body.Close()
			for _, command := range commands {
				inputs <- command
			}
		}
		if err != nil {
			fmt.Fprintf(log, "control: %v\n", err)
			time.Sleep(time.Second)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func keyEvent(key uint16, down bool) error {
	type input struct {
		Type, Pad   uint32
		Key, Scan   uint16
		Flags, Time uint32
		Extra       uintptr
		Tail        [8]byte
	}
	event := input{Type: 1, Key: key}
	if !down {
		event.Flags = 2
	}
	n, _, err := user.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&event)), unsafe.Sizeof(event))
	runtime.KeepAlive(event)
	if n != 1 {
		return fmt.Errorf("key event: %v", err)
	}
	return nil
}

func pointer(x, y int, flags uint32) error {
	w, _, _ := user.NewProc("GetSystemMetrics").Call(0)
	h, _, _ := user.NewProc("GetSystemMetrics").Call(1)
	if x < 0 || y < 0 || x >= int(w) || y >= int(h) || w < 2 || h < 2 {
		return fmt.Errorf("pointer outside screen")
	}
	return mouseEvent(int32(x*65535/int(w-1)), int32(y*65535/int(h-1)), 0, flags|0x8001)
}

func mouseEvent(x, y int32, data, flags uint32) error {
	type input struct {
		Type, Pad         uint32
		X, Y              int32
		Data, Flags, Time uint32
		Extra             uintptr
	}
	event := input{X: x, Y: y, Data: data, Flags: flags}
	n, _, err := user.NewProc("SendInput").Call(1, uintptr(unsafe.Pointer(&event)), unsafe.Sizeof(event))
	runtime.KeepAlive(event)
	if n != 1 {
		return fmt.Errorf("mouse event: %v", err)
	}
	return nil
}
