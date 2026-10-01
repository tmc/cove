package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	cf "github.com/tmc/apple/corefoundation"
	pb "github.com/tmc/cove/proto/agentpb"
)

type darwinUIBackend struct {
	elementType   func() uintptr
	valueType     func() uintptr
	once          sync.Once
	err           error
	trusted       func() bool
	create        func(int32) uintptr
	copyAttribute func(uintptr, uintptr, *uintptr) int32
	count         func(uintptr, uintptr, *int64) int32
	copyChildren  func(uintptr, uintptr, int64, int64, *uintptr) int32
	timeout       func(uintptr, float32) int32
	value         func(uintptr, int32, unsafe.Pointer) bool
	session       func() uintptr
}

func newPlatformUIBackend() userUIBackend { return &darwinUIBackend{} }

func (b *darwinUIBackend) load() {
	b.once.Do(func() {
		library, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY|purego.RTLD_LOCAL)
		if err != nil {
			b.err = err
			return
		}
		symbols := []struct {
			name     string
			function any
		}{
			{"AXIsProcessTrusted", &b.trusted}, {"AXUIElementCreateApplication", &b.create},
			{"AXUIElementCopyAttributeValue", &b.copyAttribute}, {"AXUIElementGetAttributeValueCount", &b.count},
			{"AXUIElementCopyAttributeValues", &b.copyChildren}, {"AXUIElementSetMessagingTimeout", &b.timeout},
			{"AXValueGetValue", &b.value}, {"AXUIElementGetTypeID", &b.elementType}, {"AXValueGetTypeID", &b.valueType},
		}
		for _, symbol := range symbols {
			address, err := purego.Dlsym(library, symbol.name)
			if err != nil {
				b.err = fmt.Errorf("load %s: %w", symbol.name, err)
				return
			}
			purego.RegisterFunc(symbol.function, address)
		}
		graphics, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_LOCAL)
		if err != nil {
			b.err = err
			return
		}
		address, err := purego.Dlsym(graphics, "CGSessionCopyCurrentDictionary")
		if err != nil {
			b.err = err
			return
		}
		purego.RegisterFunc(&b.session, address)
	})
}

func (b *darwinUIBackend) Status(ctx context.Context) *pb.UIStatus {
	status := &pb.UIStatus{Backend: "macos-ax", State: "unavailable", PermissionState: "unknown", SessionState: "unknown", Operations: []string{"status", "inspect", "find"}}
	if err := ctx.Err(); err != nil {
		status.Reason = err.Error()
		return status
	}
	if os.Geteuid() == 0 {
		status.State = "no_user_session"
		status.SessionState = "root"
		status.Reason = "accessibility requires the logged-in user LaunchAgent"
		return status
	}
	b.load()
	if b.err != nil {
		status.Reason = "public accessibility backend unavailable"
		return status
	}
	session := b.session()
	if session == 0 {
		status.State = "no_user_session"
		status.SessionState = "none"
		return status
	}
	defer cf.CFRelease(cf.CFTypeRef(session))
	boolean := func(name string) (bool, bool) {
		key := cf.CFStringCreateWithCString(0, name, cf.CFStringEncoding(cf.KCFStringEncodingUTF8))
		defer cf.CFRelease(cf.CFTypeRef(key))
		value := cf.CFDictionaryGetValue(cf.CFDictionaryRef(session), unsafe.Pointer(uintptr(key)))
		if value == nil || cf.CFGetTypeID(cf.CFTypeRef(uintptr(value))) != cf.CFBooleanGetTypeID() {
			return false, false
		}
		return cf.CFBooleanGetValue(cf.CFBooleanRef(uintptr(value))), true
	}
	onConsole, onConsoleKnown := boolean("kCGSessionOnConsoleKey")
	loggedIn, loginKnown := boolean("kCGSessionLoginDoneKey")
	if !onConsoleKnown || !loginKnown || !onConsole || !loggedIn {
		status.State = "no_user_session"
		status.SessionState = "inactive"
		status.Reason = "active console login not established"
		return status
	}
	locked, known := boolean("CGSSessionScreenIsLocked")
	if known && locked {
		status.State = "locked"
		status.SessionState = "locked"
		status.Reason = "unlock the guest session before accessibility inspection"
		return status
	}
	status.SessionState = "active"
	if known {
		status.SessionState = "unlocked"
	}
	if !b.trusted() {
		status.State = "permission_denied"
		status.PermissionState = "denied"
		status.Reason = "grant Accessibility to /usr/local/bin/vz-agent in guest System Settings; no prompt was requested"
		return status
	}
	status.State = "ready"
	status.PermissionState = "granted"
	if !known {
		status.Reason = "screen lock state unavailable; active console login and accessibility trust verified"
	}
	return status
}

func axReadError(code int32) error {
	if code == 0 {
		return nil
	}
	state := "read_failed"
	switch code {
	case -25211:
		state = "permission_denied"
	case -25204:
		state = "application_unresponsive"
	case -25202:
		state = "target_disappeared"
	}
	return &uiReadError{state: state, code: code}
}

func (b *darwinUIBackend) prepare(ctx context.Context, element uintptr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.DeadlineExceeded
	}
	if remaining > 250*time.Millisecond {
		remaining = 250 * time.Millisecond
	}
	// The public SDK declaration takes float, passed in the native FP register.
	return axReadError(b.timeout(element, float32(remaining.Seconds())))
}

func (b *darwinUIBackend) attribute(ctx context.Context, element uintptr, name string) (uintptr, error) {
	if err := b.prepare(ctx, element); err != nil {
		return 0, err
	}
	attribute := cf.CFStringCreateWithCString(0, name, cf.CFStringEncoding(cf.KCFStringEncodingUTF8))
	defer cf.CFRelease(cf.CFTypeRef(attribute))
	var value uintptr
	code := b.copyAttribute(element, uintptr(attribute), &value)
	if code == -25205 || code == -25212 {
		return 0, nil
	}
	if err := axReadError(code); err != nil {
		return 0, err
	}
	return value, nil
}

func (b *darwinUIBackend) stringAttribute(ctx context.Context, element uintptr, name string) (string, error) {
	value, err := b.attribute(ctx, element, name)
	if err != nil || value == 0 {
		return "", err
	}
	defer cf.CFRelease(cf.CFTypeRef(value))
	if cf.CFGetTypeID(cf.CFTypeRef(value)) != cf.CFStringGetTypeID() {
		return "", nil
	}
	if cf.CFStringGetLength(cf.CFStringRef(value)) > 1024 {
		return "", nil
	}
	var buffer [4097]byte
	if !cf.CFStringGetCString(cf.CFStringRef(value), &buffer[0], cf.CFIndex(len(buffer)), cf.CFStringEncoding(cf.KCFStringEncodingUTF8)) {
		return "", nil
	}
	return strings.TrimRight(string(buffer[:]), "\x00"), nil
}

func (b *darwinUIBackend) boolAttribute(ctx context.Context, element uintptr, name string) (*bool, error) {
	value, err := b.attribute(ctx, element, name)
	if err != nil || value == 0 {
		return nil, err
	}
	defer cf.CFRelease(cf.CFTypeRef(value))
	if cf.CFGetTypeID(cf.CFTypeRef(value)) != cf.CFBooleanGetTypeID() {
		return nil, nil
	}
	boolean := cf.CFBooleanGetValue(cf.CFBooleanRef(value))
	return &boolean, nil
}

func (b *darwinUIBackend) children(ctx context.Context, element uintptr, limit int) ([]uintptr, bool, error) {
	if err := b.prepare(ctx, element); err != nil {
		return nil, false, err
	}
	attribute := cf.CFStringCreateWithCString(0, "AXChildren", cf.CFStringEncoding(cf.KCFStringEncodingUTF8))
	defer cf.CFRelease(cf.CFTypeRef(attribute))
	var count int64
	code := b.count(element, uintptr(attribute), &count)
	if code == -25205 || code == -25212 {
		return nil, false, nil
	}
	if err := axReadError(code); err != nil {
		return nil, false, err
	}
	if count <= 0 {
		return nil, false, nil
	}
	if limit <= 0 {
		return nil, true, nil
	}
	truncated := count > int64(limit)
	if truncated {
		count = int64(limit)
	}
	if err := b.prepare(ctx, element); err != nil {
		return nil, false, err
	}
	var values uintptr
	if err := axReadError(b.copyChildren(element, uintptr(attribute), 0, count, &values)); err != nil {
		return nil, false, err
	}
	if values == 0 {
		return nil, truncated, nil
	}
	defer cf.CFRelease(cf.CFTypeRef(values))
	if cf.CFGetTypeID(cf.CFTypeRef(values)) != cf.CFArrayGetTypeID() {
		return nil, false, fmt.Errorf("children attribute is not an array")
	}
	actual := int(cf.CFArrayGetCount(cf.CFArrayRef(values)))
	if actual > limit {
		actual = limit
		truncated = true
	}
	var children []uintptr
	for i := 0; i < actual; i++ {
		value := uintptr(cf.CFArrayGetValueAtIndex(cf.CFArrayRef(values), cf.CFIndex(i)))
		if value != 0 {
			if uintptr(cf.CFGetTypeID(cf.CFTypeRef(value))) != b.elementType() {
				for _, child := range children {
					cf.CFRelease(cf.CFTypeRef(child))
				}
				return nil, false, fmt.Errorf("child is not an accessibility element")
			}
			cf.CFRetain(cf.CFTypeRef(value))
			children = append(children, value)
		}
	}
	return children, truncated, nil
}

func (b *darwinUIBackend) Inspect(ctx context.Context, request *pb.UIRequest) ([]*pb.UINode, bool, string, error) {
	b.load()
	if b.err != nil {
		return nil, false, "", b.err
	}
	application := b.create(request.Pid)
	if application == 0 {
		return nil, false, "", &uiReadError{state: "application_not_running"}
	}
	type pending struct {
		ref    uintptr
		depth  uint32
		parent string
	}
	queue := []pending{{ref: application}}
	defer func() {
		for _, item := range queue {
			cf.CFRelease(cf.CFTypeRef(item.ref))
		}
	}()
	var nodes []*pb.UINode
	truncated := false
	reason := ""
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		node := &pb.UINode{Handle: strconv.Itoa(len(nodes) + 1), ParentHandle: item.parent, ValueOmitted: true}
		read := func() error {
			var err error
			if node.Role, err = b.stringAttribute(ctx, item.ref, "AXRole"); err != nil {
				return err
			}
			if node.Subrole, err = b.stringAttribute(ctx, item.ref, "AXSubrole"); err != nil {
				return err
			}
			if node.Identifier, err = b.stringAttribute(ctx, item.ref, "AXIdentifier"); err != nil {
				return err
			}
			if node.Role != "AXStaticText" && node.Role != "AXTextField" && node.Role != "AXTextArea" && node.Subrole != "AXSecureTextField" {
				if node.Label, err = b.stringAttribute(ctx, item.ref, "AXTitle"); err != nil {
					return err
				}
			}
			if node.Enabled, err = b.boolAttribute(ctx, item.ref, "AXEnabled"); err != nil {
				return err
			}
			if node.Focused, err = b.boolAttribute(ctx, item.ref, "AXFocused"); err != nil {
				return err
			}
			var position, size [2]float64
			values := []struct {
				name   string
				kind   int32
				target *[2]float64
			}{{"AXPosition", 1, &position}, {"AXSize", 2, &size}}
			hasBounds := true
			for _, attribute := range values {
				value, err := b.attribute(ctx, item.ref, attribute.name)
				if err != nil {
					return err
				}
				if value == 0 {
					hasBounds = false
					continue
				}
				valid := false
				if uintptr(cf.CFGetTypeID(cf.CFTypeRef(value))) == b.valueType() {
					valid = b.value(value, attribute.kind, unsafe.Pointer(attribute.target))
				}
				cf.CFRelease(cf.CFTypeRef(value))
				if !valid {
					hasBounds = false
				}
			}
			if hasBounds {
				node.Bounds = &pb.UIBounds{X: position[0], Y: position[1], Width: size[0], Height: size[1]}
			}
			budget := int(request.MaxNodes) - len(nodes) - len(queue) - 1
			if item.depth >= request.MaxDepth {
				budget = 0
			}
			children, cut, err := b.children(ctx, item.ref, budget)
			if err != nil {
				return err
			}
			if cut {
				truncated = true
				reason = "depth_or_node_limit"
			}
			for _, child := range children {
				queue = append(queue, pending{ref: child, depth: item.depth + 1, parent: node.Handle})
			}
			return nil
		}
		err := read()
		cf.CFRelease(cf.CFTypeRef(item.ref))
		if err != nil {
			return nil, false, "", err
		}
		nodes = append(nodes, node)
	}
	return nodes, truncated, reason, nil
}
