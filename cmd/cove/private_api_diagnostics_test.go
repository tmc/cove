//go:build darwin && arm64

package main

import (
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/security"
	"testing"
	"unsafe"
)

func requirePrivateVirtualization(t *testing.T) {
	t.Helper()
	task := security.SecTaskCreateFromSelf(0)
	if task == 0 {
		t.Fatal("create security task")
	}
	defer corefoundation.CFRelease(unsafe.Pointer(uintptr(task)))
	key := foundation.NewStringWithString("com.apple.private.virtualization")
	value := security.SecTaskCopyValueForEntitlement(task, corefoundation.CFStringRef(key.ID), nil)
	if value == nil {
		t.Skip("requires com.apple.private.virtualization entitlement")
	}
	defer corefoundation.CFRelease(value)
	if corefoundation.CFGetTypeID(value) != corefoundation.CFBooleanGetTypeID() || !corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef(uintptr(value))) {
		t.Skip("requires com.apple.private.virtualization entitlement")
	}
}

func TestPrivateAPI_StateDescription(t *testing.T) {
	t.Skip("_stateDescription causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_NameGetSet(t *testing.T) {
	requirePrivateVirtualization(t)
	_, privVM, queue := requireLiveVM(t)

	if !objc.RespondsToSelector(privVM.ID, objc.Sel("_name")) || !objc.RespondsToSelector(privVM.ID, objc.Sel("_setName:")) {
		t.Skip("_name accessors unavailable")
	}

	testName := "diagnostics-test-vm"
	var initialName, newName string
	queue.Sync(func() {
		nameID := objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		initialName = foundation.NSStringFromID(nameID).String()
		objc.Send[objc.ID](privVM.ID, objc.Sel("_setName:"), objc.String(testName))
		nameID = objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		newName = foundation.NSStringFromID(nameID).String()
	})
	t.Logf("_name: %q -> %q", initialName, newName)

	if newName != testName {
		t.Errorf("_name after Set_name(%q) = %q", testName, newName)
	}
}

func TestPrivateAPI_CrashContextMessage(t *testing.T) {
	_, privVM, queue := requireLiveVM(t)

	if !objc.RespondsToSelector(privVM.ID, objc.Sel("_crashContextMessage")) || !objc.RespondsToSelector(privVM.ID, objc.Sel("_setCrashContextMessage:")) {
		t.Skip("_crashContextMessage accessors unavailable")
	}

	testMsg := "test-crash-context-diagnostics"
	var initialMsg, newMsg string
	queue.Sync(func() {
		msgID := objc.Send[objc.ID](privVM.ID, objc.Sel("_crashContextMessage"))
		initialMsg = foundation.NSStringFromID(msgID).String()
		objc.Send[objc.ID](privVM.ID, objc.Sel("_setCrashContextMessage:"), objc.String(testMsg))
		msgID = objc.Send[objc.ID](privVM.ID, objc.Sel("_crashContextMessage"))
		newMsg = foundation.NSStringFromID(msgID).String()
	})
	t.Logf("_crashContextMessage: %q -> %q", initialMsg, newMsg)

	if newMsg != testMsg {
		t.Errorf("_crashContextMessage after Set = %q, want %q", newMsg, testMsg)
	}
}

func TestPrivateAPI_ServiceProcessIdentifier(t *testing.T) {
	t.Skip("_serviceProcessIdentifier causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_ShouldSendHIDReports(t *testing.T) {
	t.Skip("_shouldSendHIDReports causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_CanCreateCore(t *testing.T) {
	t.Skip("_canCreateCore causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_DeviceArrays(t *testing.T) {
	t.Skip("device array accessors cause SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_PublicState(t *testing.T) {
	t.Skip("State() causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_DiagnosticsSummary(t *testing.T) {
	t.Skip("_stateDescription causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}
