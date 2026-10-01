package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

var copyDropClassCount atomic.Uint64

func newCopyDropVMView(vmDirectory string) vz.VZVirtualMachineView {
	var busy bool
	accept := func(self, sender objc.ID) uint64 {
		view := vz.VZVirtualMachineViewFromID(self)
		if busy || view.VirtualMachine().State() != vz.VZVirtualMachineStateRunning || len(copyDropPaths(sender)) == 0 {
			return 0
		}
		return uint64(appkit.NSDragOperationCopy)
	}
	cls, err := objc.RegisterClass(fmt.Sprintf("CoveCopyDropView_%d", copyDropClassCount.Add(1)), objc.GetClass("VZVirtualMachineView"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("draggingEntered:"), Fn: func(self objc.ID, _ objc.SEL, sender objc.ID) uint64 { return accept(self, sender) }},
		{Cmd: objc.RegisterName("draggingUpdated:"), Fn: func(self objc.ID, _ objc.SEL, sender objc.ID) uint64 { return accept(self, sender) }},
		{Cmd: objc.RegisterName("prepareForDragOperation:"), Fn: func(self objc.ID, _ objc.SEL, sender objc.ID) bool { return accept(self, sender) != 0 }},
		{Cmd: objc.RegisterName("performDragOperation:"), Fn: func(self objc.ID, _ objc.SEL, sender objc.ID) bool {
			if accept(self, sender) == 0 {
				return false
			}
			paths := copyDropPaths(sender)
			window := appkit.NSWindowFromID(objc.Send[objc.ID](self, objc.Sel("window")))
			busy = true
			startCopyDrop(window, vmDirectory, paths, func() { busy = false })
			return true
		}},
	})
	if err != nil {
		return vz.NewVZVirtualMachineView()
	}
	id := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc")), objc.Sel("init"))
	view := vz.VZVirtualMachineViewFromID(id)
	vmViewAsNSView(view).RegisterForDraggedTypes([]string{"public.file-url", "NSFilenamesPboardType"})
	return view
}

func copyDropPaths(sender objc.ID) []string {
	pb := objc.Send[objc.ID](sender, objc.Sel("draggingPasteboard"))
	classes := foundation.NewArrayWithObject(objectivec.ObjectFromID(objc.ID(objc.GetClass("NSURL"))))
	urls := objc.Send[objc.ID](pb, objc.Sel("readObjectsForClasses:options:"), classes.ID, objc.ID(0))
	var paths []string
	for _, u := range objc.NSArrayToSlice(urls) {
		if !objc.Send[bool](u, objc.Sel("isFileURL")) {
			continue
		}
		p := objc.IDToString(objc.Send[objc.ID](u, objc.Sel("path")))
		if filepath.IsAbs(p) && filepath.Base(p) != string(filepath.Separator) {
			paths = append(paths, p)
		}
	}
	return paths
}

func copyDropDestinations(folder string, paths []string) []string {
	used := make(map[string]bool)
	dests := make([]string, len(paths))
	for i, hostPath := range paths {
		base := filepath.Base(filepath.Clean(hostPath))
		name := base
		ext := filepath.Ext(base)
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), n, ext)
		}
		used[name] = true
		dests[i] = path.Join("~/Downloads", folder, name)
	}
	return dests
}

func startCopyDrop(window appkit.NSWindow, vmDirectory string, paths []string, finished func()) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		finished()
		reportGUIError(window, "Could not copy files", err)
		return
	}
	folder := fmt.Sprintf("Cove-%x", nonce)
	destinations := copyDropDestinations(folder, paths)
	ctx, cancel := context.WithCancel(context.Background())
	alert := appkit.NewNSAlert()
	alert.SetMessageText("Copying to guest Downloads")
	alert.SetInformativeText("Connecting to the signed-in guest user…")
	alert.AddButtonWithTitle("Cancel")
	var completed bool
	alert.BeginSheetModalForWindowCompletionHandler(window, func(appkit.NSModalResponse) {
		if !completed {
			cancel()
		}
	})
	go func() {
		defer cancel()
		client := NewControlClient(filepath.Join(vmDirectory, "control.sock"))
		var copyErr error
		for i, p := range paths {
			if ctx.Err() != nil {
				copyErr = ctx.Err()
				break
			}
			dest := destinations[i]
			req := &controlpb.ControlRequest{Type: "agent-cp-stream", Command: &controlpb.ControlRequest_AgentCp{AgentCp: &controlpb.AgentCopyCommand{HostPath: p, GuestPath: dest, ToGuest: true}}}
			resp, err := client.SendRequestProgressCtx(ctx, req, func(status string) {
				DispatchAsyncMain(func() {
					if !completed {
						alert.SetInformativeText(fmt.Sprintf("File %d of %d: %s\n%s", i+1, len(paths), filepath.Base(p), status))
						alert.Layout()
					}
				})
			})
			if err != nil {
				copyErr = err
				break
			}
			if !resp.Success {
				copyErr = errors.New(resp.Error)
				break
			}
		}
		DispatchAsyncMain(func() {
			completed = true
			if alert.Window().SheetParent().GetID() == window.ID {
				window.EndSheet(alert.Window())
			}
			finished()
			if copyErr != nil {
				if !errors.Is(copyErr, context.Canceled) {
					reportGUIError(window, "Copy did not finish", fmt.Errorf("%w; completed files, if any, are in Downloads/%s", copyErr, folder))
				}
				return
			}
			done := appkit.NewNSAlert()
			done.SetMessageText("Files copied")
			done.SetInformativeText(fmt.Sprintf("%d files saved in the guest's Downloads/%s.", len(paths), folder))
			done.AddButtonWithTitle("OK")
			done.BeginSheetModalForWindowCompletionHandler(window, nil)
		})
	}()
}
