// drag_drop.go - Host-to-guest file transfer via drag-and-drop.
//
// Registers drag types on the VM view. Files dropped onto the VM window
// are copied to a "drops" directory shared with the guest via VirtioFS.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

// dropsVirtioFSTag is the VirtioFS tag for the drops directory.
const dropsVirtioFSTag = "drops"

// VMDragDrop manages drag-and-drop file transfer into the VM.
type VMDragDrop struct {
	dropsDir  string
	vmDir     string
	vm        vz.VZVirtualMachine
	vmQueue   dispatch.Queue
	ctlServer *ControlServer // for guest agent access (set after construction)
}

var nsPasteboardTypeFileURL objc.ID

func initPasteboardTypes() {
	if nsPasteboardTypeFileURL != 0 {
		return
	}
	lib, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY)
	if err != nil {
		return
	}
	// NSPasteboardTypeFileURL is a global NSString* variable.
	// Dlsym returns the address of the pointer, so we dereference twice:
	// sym = &NSPasteboardTypeFileURL (address of the global)
	// *sym = NSPasteboardTypeFileURL (the NSString* value)
	sym, err := purego.Dlsym(lib, "NSPasteboardTypeFileURL")
	if err != nil {
		return
	}
	nsPasteboardTypeFileURL = *(*objc.ID)(unsafe.Pointer(sym))
}

// dragDropHandler is a package-level reference for the ObjC callback functions.
var dragDropHandler *VMDragDrop

// SetupDragDrop configures drag-and-drop on the VM view.
func SetupDragDrop(vmView vz.VZVirtualMachineView, vm vz.VZVirtualMachine, vmQueue dispatch.Queue, vmDirectory string) *VMDragDrop {
	initPasteboardTypes()
	if nsPasteboardTypeFileURL == 0 {
		fmt.Println("Warning: drag-drop disabled (NSPasteboardTypeFileURL unavailable)")
		return nil
	}

	dropsDir := filepath.Join(vmDirectory, "drops")
	if err := os.MkdirAll(dropsDir, 0755); err != nil {
		fmt.Printf("warning: drag-drop: could not create drops dir: %v\n", err)
		return nil
	}

	dd := &VMDragDrop{
		dropsDir: dropsDir,
		vmDir:    vmDirectory,
		vm:       vm,
		vmQueue:  vmQueue,
	}
	dragDropHandler = dd

	// Add NSDraggingDestination methods to VZVirtualMachineView's class
	// at runtime using class_addMethod. This lets the VM view handle
	// drag operations directly without an overlay.
	addDragMethodsToView(vmView)

	// Register the VM view for file URL drag types.
	typeArr := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")),
		objc.Sel("arrayWithObject:"),
		nsPasteboardTypeFileURL,
	)
	objc.Send[struct{}](vmView.ID, objc.Sel("registerForDraggedTypes:"), typeArr)

	dd.verifyDropsDevice()

	fmt.Printf("Drag-and-drop: drop files onto window → %s (guest: /Volumes/%s)\n", dropsDir, dropsVirtioFSTag)
	return dd
}

func addDragMethodsToView(vmView vz.VZVirtualMachineView) {
	cls := objc.Send[objc.Class](vmView.ID, objc.Sel("class"))

	enteredIMP := purego.NewCallback(dragDraggingEntered)
	objc.AddMethod(cls, objc.RegisterName("draggingEntered:"), enteredIMP, "Q@:@")

	updatedIMP := purego.NewCallback(dragDraggingUpdated)
	objc.AddMethod(cls, objc.RegisterName("draggingUpdated:"), updatedIMP, "Q@:@")

	performIMP := purego.NewCallback(dragPerformDragOperation)
	objc.AddMethod(cls, objc.RegisterName("performDragOperation:"), performIMP, "B@:@")
}

func dragDraggingEntered(_ objc.ID, _ objc.SEL, sender objc.ID) uintptr {
	return 2 // NSDragOperationCopy
}

func dragDraggingUpdated(_ objc.ID, _ objc.SEL, sender objc.ID) uintptr {
	return 2 // NSDragOperationCopy
}

func dragPerformDragOperation(_ objc.ID, _ objc.SEL, sender objc.ID) bool {
	dd := dragDropHandler
	if dd == nil {
		return false
	}

	pb := objc.Send[objc.ID](sender, objc.Sel("draggingPasteboard"))

	urlClass := objc.ID(objc.GetClass("NSURL"))
	classArr := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")),
		objc.Sel("arrayWithObject:"),
		urlClass,
	)
	urls := objc.Send[objc.ID](pb, objc.Sel("readObjectsForClasses:options:"), classArr, objc.ID(0))
	if urls == 0 {
		return false
	}

	count := int(objc.Send[uint](urls, objc.Sel("count")))
	if count == 0 {
		return false
	}

	copied := 0
	var guestPaths []string
	for i := range count {
		urlObj := objc.Send[objc.ID](urls, objc.Sel("objectAtIndex:"), uint(i))
		// NSURL.path returns NSString*; get the C string via UTF8String.
		nsPath := objc.Send[objc.ID](urlObj, objc.Sel("path"))
		path := objc.GoString(objc.Send[*byte](nsPath, objc.Sel("UTF8String")))
		if path == "" {
			continue
		}

		dst := filepath.Join(dd.dropsDir, filepath.Base(path))
		if err := copyDropItem(path, dst); err != nil {
			fmt.Printf("Drag-drop: failed to copy %s: %v\n", filepath.Base(path), err)
			continue
		}
		fmt.Printf("Drag-drop: %s → %s\n", filepath.Base(path), dst)
		guestPaths = append(guestPaths, fmt.Sprintf("/Volumes/%s/%s", dropsVirtioFSTag, filepath.Base(path)))
		copied++
	}

	if copied > 0 {
		fmt.Printf("Drag-drop: %d file(s) available at /Volumes/%s/ in guest\n", copied, dropsVirtioFSTag)
		dd.deliverToGuest(guestPaths)
	}
	return copied > 0
}

// verifyDropsDevice checks that the dedicated drops VirtioFS device exists.
func (dd *VMDragDrop) verifyDropsDevice() {
	DispatchAsyncQueue(dd.vmQueue, func() {
		state := vz.VZVirtualMachineState(dd.vm.State())
		if state != vz.VZVirtualMachineStateRunning && state != vz.VZVirtualMachineStatePaused {
			return
		}

		devices := dd.vm.DirectorySharingDevices()
		for _, d := range devices {
			dev := vz.VZVirtioFileSystemDeviceFromID(d.ID)
			if dev.Tag() == dropsVirtioFSTag {
				return // device exists
			}
		}
		fmt.Println("Warning: drag-drop: no VirtioFS device with tag \"drops\" found")
	})
}

// SetControlServer sets the control server reference for guest delivery.
// Called after both the VMDragDrop and ControlServer are constructed.
func (dd *VMDragDrop) SetControlServer(cs *ControlServer) {
	dd.ctlServer = cs
}

// deliverToGuest uses the guest agent to type dropped file paths into
// the frontmost application via AppleScript keystroke. The osascript
// must run as the logged-in user (not root) so that TCC permissions
// and the user's GUI session are accessible.
func (dd *VMDragDrop) deliverToGuest(guestPaths []string) {
	if dd.ctlServer == nil {
		return
	}
	go func() {
		// Grab agent ref under mutex, then release so we don't block
		// other control socket operations during the exec call.
		dd.ctlServer.mu.Lock()
		if err := dd.ctlServer.ensureAgent(); err != nil {
			dd.ctlServer.mu.Unlock()
			fmt.Printf("Drag-drop: agent not available for guest delivery: %v\n", err)
			return
		}
		agent := dd.ctlServer.agent
		dd.ctlServer.mu.Unlock()

		// Determine the logged-in console user so osascript runs in
		// their GUI session with their TCC permissions.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, err := agent.Exec(ctx, []string{"stat", "-f", "%Su", "/dev/console"}, nil, "")
		cancel()
		consoleUser := ""
		if err == nil && result.ExitCode == 0 {
			consoleUser = strings.TrimSpace(string(result.Stdout))
		}
		if consoleUser == "" || consoleUser == "root" {
			fmt.Printf("Drag-drop: no console user found, skipping guest delivery\n")
			return
		}

		script := buildDropDeliveryScript(guestPaths)
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		result, err = agent.ExecAs(ctx, consoleUser, []string{"osascript", "-e", script}, nil, "")
		cancel()
		if err != nil {
			fmt.Printf("Drag-drop: guest delivery failed: %v\n", err)
		} else if result.ExitCode != 0 {
			stderr := strings.TrimSpace(string(result.Stderr))
			if strings.Contains(stderr, "accessibility") || strings.Contains(stderr, "not allowed") {
				fmt.Printf("Drag-drop: guest needs Accessibility permission for System Events\n")
				fmt.Printf("  Grant in: System Settings → Privacy & Security → Accessibility\n")
			} else {
				fmt.Printf("Drag-drop: guest delivery error: %s\n", stderr)
			}
		} else {
			fmt.Printf("Drag-drop: delivered %d path(s) to guest app\n", len(guestPaths))
		}
	}()
}

// buildDropDeliveryScript returns an AppleScript that types file paths
// into whatever app is frontmost in the guest. Multiple paths are
// shell-quoted and space-separated in a single keystroke to avoid
// interleaving issues.
func buildDropDeliveryScript(guestPaths []string) string {
	var quoted []string
	for _, p := range guestPaths {
		// Shell-quote paths that contain spaces or special characters.
		if strings.ContainsAny(p, " \t'\"\\$`!#&|;(){}[]<>?*~") {
			quoted = append(quoted, "'"+strings.ReplaceAll(p, "'", "'\\''")+"'")
		} else {
			quoted = append(quoted, p)
		}
	}
	text := strings.Join(quoted, " ")
	// Escape for AppleScript double-quoted string.
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, `"`, `\"`)
	return fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, text)
}

func copyDropItem(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	dst = uniqueDropPath(dst)

	if info.IsDir() {
		return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if fi.IsDir() {
				return os.MkdirAll(target, fi.Mode())
			}
			return copyDropFile(path, target)
		})
	}
	return copyDropFile(src, dst)
}

func copyDropFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func uniqueDropPath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}
