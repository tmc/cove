package diskimages2

import (
	"fmt"
	"os/exec"
	"unsafe"

	di2 "github.com/tmc/appledocs/generated/diskimages2"
	"github.com/tmc/appledocs/generated/foundation"
	"github.com/tmc/appledocs/generated/objc"
)

// Attach attaches a disk image at the given path using DIAttachParams.
// Returns a DeviceHandle with the BSD device name. The caller must call
// [DeviceHandle.Release] when done (or use [DetachHandle]).
func Attach(path string, opts AttachOptions) (*DeviceHandle, error) {
	if err := ensureLoaded(); err != nil {
		return nil, err
	}

	url := foundation.FileURL(path)

	params, err := di2.NewDIAttachParamsWithURLError(url)
	if err != nil {
		return nil, fmt.Errorf("diskimages2: init attach params: %w", err)
	}
	if params.ID == 0 {
		return nil, fmt.Errorf("diskimages2: DIAttachParams initWithURL returned nil")
	}

	params.SetAutoMount(opts.AutoMount)
	if opts.FileMode != 0 {
		params.SetFileMode(opts.FileMode)
	}

	handleIface, err := params.NewAttachWithError()
	if err != nil {
		return nil, fmt.Errorf("diskimages2: attach: %w", err)
	}
	handle, ok := handleIface.(di2.DIDeviceHandle)
	if !ok {
		return nil, fmt.Errorf("diskimages2: unexpected handle type %T", handleIface)
	}
	objc.Send[objc.ID](handle.ID, objc.Sel("retain"))

	if _, err := handle.WaitForDeviceWithError(); err != nil {
		handle.Release()
		return nil, fmt.Errorf("diskimages2: wait for device: %w", err)
	}

	return &DeviceHandle{
		BSDName:    handle.BSDName(),
		RegEntryID: handle.RegEntryID(),
		handle:     handle,
	}, nil
}

// AttachSimple attaches a disk image using DICommonAttach's convenience method.
// Returns the BSD device name (e.g., "disk27").
func AttachSimple(path string, readOnly, autoMount bool) (bsdName string, err error) {
	if err := ensureLoaded(); err != nil {
		return "", err
	}

	url := foundation.FileURL(path)

	// The generated binding for DICommonAttach treats BSDName as a string
	// param, but the ObjC method uses NSString ** (output parameter).
	// Call the raw selector for correct semantics.
	cls := objc.ID(objc.GetClass("DICommonAttach"))
	var bsdNamePtr objc.ID
	var errPtr objc.ID
	ok := objc.Send[bool](
		cls,
		objc.Sel("diskImageAttach:readOnly:autoMount:BSDName:error:"),
		url, readOnly, autoMount, unsafe.Pointer(&bsdNamePtr), unsafe.Pointer(&errPtr),
	)
	if !ok {
		if errPtr != 0 {
			nsErr := foundation.NSErrorFrom(errPtr)
			return "", fmt.Errorf("diskimages2: %s", nsErr.LocalizedDescription())
		}
		return "", fmt.Errorf("diskimages2: diskImageAttach failed")
	}

	if bsdNamePtr != 0 {
		return foundation.NSStringFrom(bsdNamePtr).String(), nil
	}
	return "", nil
}

// Detach detaches a previously attached disk image by BSD name.
// DiskImages2 does not expose a detach API; this calls hdiutil detach.
func Detach(bsdName string) error {
	return exec.Command("hdiutil", "detach", "/dev/"+bsdName, "-force").Run()
}

// DetachHandle detaches using an existing DeviceHandle, then releases it.
func DetachHandle(h *DeviceHandle) error {
	if h == nil {
		return nil
	}
	err := Detach(h.BSDName)
	h.Release()
	return err
}
