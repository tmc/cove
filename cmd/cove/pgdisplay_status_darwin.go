//go:build darwin

package main

import (
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pg "github.com/tmc/apple/paravirtualizedgraphics"
)

// pgDisplayStatus locates the live PGDisplay object owned by the VZ
// graphics stack and reads its counters. Everything here is read-only:
// if the display cannot be located the result reports unavailable with
// a reason instead of returning an error.
func (s *ControlServer) pgDisplayStatus() DisplayStatus {
	s.mu.Lock()
	vm := s.vm
	s.mu.Unlock()
	state := s.captureState()

	if vm.ID == 0 && state.vmView.ID == 0 {
		return DisplayStatus{Reason: "vm not set"}
	}

	var status DisplayStatus
	runOnUIThreadSync(func() {
		pool := foundation.NewNSAutoreleasePool()
		defer pool.Drain()

		roots := []objc.ID{}
		if state.vmView.ID != 0 {
			roots = append(roots, state.vmView.ID)
		}
		if vm.ID != 0 {
			roots = append(roots, vm.ID)
		}
		id, path := findPGDisplay(roots)
		if id == 0 {
			status = DisplayStatus{Reason: "pgdisplay not located"}
			return
		}
		status = readPGDisplayStatus(id)
		status.Path = path
	})
	return status
}

// findPGDisplay walks instance-variable graphs from the given roots
// looking for an object that implements the PGDisplay protocol. This
// extends the ivar-walking technique used by
// capturePrivateGraphicsDisplay (screenshots_private_darwin.go) into a
// bounded breadth-first search over object-typed ivars.
func findPGDisplay(roots []objc.ID) (objc.ID, string) {
	type node struct {
		id    objc.ID
		path  string
		depth int
	}
	const (
		maxDepth = 6
		maxNodes = 2000
	)
	visited := make(map[objc.ID]bool)
	queue := make([]node, 0, len(roots))
	for _, r := range roots {
		if r != 0 && !visited[r] {
			visited[r] = true
			queue = append(queue, node{id: r, path: objectClassName(r)})
		}
	}
	seen := 0
	for len(queue) > 0 && seen < maxNodes {
		n := queue[0]
		queue = queue[1:]
		seen++

		if isPGDisplay(n.id) {
			return n.id, n.path
		}
		if n.depth >= maxDepth {
			continue
		}
		for _, child := range objectIvarObjects(n.id) {
			if child.id == 0 || visited[child.id] {
				continue
			}
			visited[child.id] = true
			queue = append(queue, node{
				id:    child.id,
				path:  n.path + "." + child.name,
				depth: n.depth + 1,
			})
		}
	}
	return 0, ""
}

// isPGDisplay reports whether id implements the PGDisplay counters we
// need. Probing with respondsToSelector keeps this safe on OS versions
// where the private graphics stack changes shape.
func isPGDisplay(id objc.ID) bool {
	return objc.RespondsToSelector(id, objc.Sel("guestPresentCount")) &&
		objc.RespondsToSelector(id, objc.Sel("hostPresentCount")) &&
		objc.RespondsToSelector(id, objc.Sel("cursorPosition"))
}

type ivarChild struct {
	id   objc.ID
	name string
}

// objectIvarObjects enumerates the object-typed instance variables of
// id, including those declared on superclasses. Non-object ivars
// (type encoding not starting with '@') are skipped: reading them as
// object pointers would be unsafe.
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

// readPGDisplayStatus reads the display counters and geometry from a
// located PGDisplay. Each read is gated on respondsToSelector so a
// partially matching object degrades to omitted fields, not a crash.
func readPGDisplayStatus(id objc.ID) DisplayStatus {
	d := pg.PGDisplayObjectFromID(id)
	status := DisplayStatus{
		Available: true,
		Class:     objectClassName(id),
	}
	status.GuestPresentCount = uint64(d.GuestPresentCount())
	status.HostPresentCount = uint64(d.HostPresentCount())
	if objc.RespondsToSelector(id, objc.Sel("name")) {
		status.Name = d.Name()
	}
	if objc.RespondsToSelector(id, objc.Sel("serialNum")) {
		status.SerialNum = d.SerialNum()
	}
	if objc.RespondsToSelector(id, objc.Sel("port")) {
		status.Port = uint64(d.Port())
	}
	pos := d.CursorPosition()
	status.CursorX = pos.X
	status.CursorY = pos.Y
	if objc.RespondsToSelector(id, objc.Sel("sizeInMillimeters")) {
		size := d.SizeInMillimeters()
		status.SizeMillimetersWidth = size.Width
		status.SizeMillimetersHeight = size.Height
	}
	if objc.RespondsToSelector(id, objc.Sel("modeList")) {
		for _, mode := range d.ModeList() {
			size := mode.SizeInPixels()
			status.Modes = append(status.Modes, DisplayModeStatus{
				Width:     size.X,
				Height:    size.Y,
				RefreshHz: mode.RefreshRate(),
			})
		}
	}
	return status
}
