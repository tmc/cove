//go:build darwin

package main

import (
	"unsafe"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
)

// Private object graphs do not provide a safe, retained display handle.
// A diagnostic request must not risk terminating the VM runtime.
func (s *ControlServer) pgDisplayStatus() DisplayStatus {
	status := DisplayStatus{Reason: "private display observation disabled: no safe retained display handle"}
	if s == nil {
		return status
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	const maxDisplays = 16
	displays := s.runConfig.Displays
	if len(displays) == 0 {
		return status
	}
	status.ConfigurationSource = "run_config"
	if len(displays) > maxDisplays {
		displays = displays[:maxDisplays]
		status.ConfiguredDisplaysTruncated = true
	}
	for _, display := range displays {
		status.ConfiguredDisplays = append(status.ConfiguredDisplays, ConfiguredDisplayStatus{
			Width:         display.Width,
			Height:        display.Height,
			PixelsPerInch: display.PPI,
		})
	}
	return status
}

type ivarChild struct {
	id   objc.ID
	name string
}

// objectIvarObjects is used by the opt-in private framebuffer backend.
// Object type encoding does not guarantee that a field holds a valid,
// retained object. These raw pointers must not be used for diagnostics.
func objectIvarObjects(id objc.ID) []ivarChild {
	obj := objectivec.ObjectFromID(id)
	var children []ivarChild
	for cls := objectivec.Object_getClass(obj); cls != 0; cls = objectivec.Class_getSuperclass(cls) {
		name := objc.GoString(objectivec.Class_getName(cls))
		if name == "NSObject" || name == "NSResponder" || name == "NSView" {
			break
		}
		var count uint32
		list := objectivec.Class_copyIvarList(cls, &count)
		if list == 0 || count == 0 {
			continue
		}
		// list is a C-allocated array handle (objectivec.Ivar is a
		// uintptr alias); reinterpret the variable's storage so vet
		// does not flag a uintptr-to-Pointer conversion.
		listPtr := *(*unsafe.Pointer)(unsafe.Pointer(&list))
		ivars := unsafe.Slice((*objectivec.Ivar)(listPtr), int(count))
		for _, iv := range ivars {
			enc := objc.GoString(objectivec.Ivar_getTypeEncoding(iv))
			if len(enc) == 0 || enc[0] != '@' {
				continue
			}
			child := objectivec.Object_getIvar(obj, iv)
			if child.ID == 0 {
				continue
			}
			children = append(children, ivarChild{
				id:   child.ID,
				name: objc.GoString(objectivec.Ivar_getName(iv)),
			})
		}
	}
	return children
}

func objectClassName(id objc.ID) string {
	obj := objectivec.ObjectFromID(id)
	return objc.GoString(objectivec.Class_getName(objectivec.Object_getClass(obj)))
}
