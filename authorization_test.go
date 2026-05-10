package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPreWarmUsesAuthorizationCreate(t *testing.T) {
	oldAuthInitialized := authInitialized
	oldAuthCreate := authCreate
	oldAuthExecute := authExecute
	oldAuthFree := authFree
	defer func() {
		authInitialized = oldAuthInitialized
		authCreate = oldAuthCreate
		authExecute = oldAuthExecute
		authFree = oldAuthFree
	}()

	var createCalled bool
	var freeCalled bool
	authInitialized = true
	authCreate = func(_ uintptr, _ uintptr, flags uint32, authRef *uintptr) int32 {
		createCalled = true
		if flags&(kAuthorizationFlagInteractionAllowed|kAuthorizationFlagExtendRights|kAuthorizationFlagPreAuthorize) == 0 {
			t.Fatalf("flags = %#x, missing expected authorization flags", flags)
		}
		*authRef = 42
		return 0
	}
	authExecute = func(uintptr, uintptr, uint32, uintptr, *uintptr) int32 { return 0 }
	authFree = func(authRef uintptr, flags uint32) int32 {
		freeCalled = true
		if authRef != 42 {
			t.Fatalf("authRef = %d, want 42", authRef)
		}
		if flags != kAuthorizationFlagDestroyRights {
			t.Fatalf("free flags = %#x, want destroy rights", flags)
		}
		return 0
	}

	if err := PreWarm(); err != nil {
		t.Fatalf("PreWarm: %v", err)
	}
	if !createCalled || !freeCalled {
		t.Fatalf("create/free called = %v/%v, want true/true", createCalled, freeCalled)
	}
}

func TestRunElevatedManifestNativeTimesOutWithoutPrompt(t *testing.T) {
	restore := stubAuthorizationCreate(t, false)
	defer restore()

	err := runElevatedManifestNative("/tmp/manifest.json", "abc123", "Provision VM")
	if err == nil {
		t.Fatal("runElevatedManifestNative succeeded, want timeout")
	}
	if !strings.Contains(err.Error(), "AuthorizationCreate wedged after") {
		t.Fatalf("error = %v, want AuthorizationCreate timeout", err)
	}
}

func TestRunElevatedManifestNativeRefusesNoninteractive(t *testing.T) {
	oldTTY := authorizationStdinIsTTY
	oldAuthCreate := authCreate
	t.Cleanup(func() { authorizationStdinIsTTY = oldTTY })
	authorizationStdinIsTTY = func() bool { return false }
	authCreate = func(uintptr, uintptr, uint32, *uintptr) int32 {
		t.Fatal("authCreate called for noninteractive stdin")
		return 0
	}
	t.Cleanup(func() { authCreate = oldAuthCreate })

	err := runElevatedManifestNative("/tmp/manifest.json", "abc123", "Provision VM")
	if err == nil {
		t.Fatal("runElevatedManifestNative succeeded, want noninteractive error")
	}
	if !strings.Contains(err.Error(), "native authorization requires an interactive terminal") {
		t.Fatalf("error = %v, want interactive terminal refusal", err)
	}
}

func TestRunElevatedManifestNativeTimesOutWithPromptPending(t *testing.T) {
	restore := stubAuthorizationCreate(t, true)
	defer restore()

	err := runElevatedManifestNative("/tmp/manifest.json", "abc123", "Provision VM")
	if err == nil {
		t.Fatal("runElevatedManifestNative succeeded, want prompt timeout")
	}
	if !strings.Contains(err.Error(), "authorization dialog still pending after") {
		t.Fatalf("error = %v, want prompt-pending timeout", err)
	}
}

func TestRunElevatedManifestNativeTimesOutExecuteWithoutPrompt(t *testing.T) {
	restore := stubAuthorizationExecute(t, false)
	defer restore()

	err := runElevatedManifestNative("/tmp/manifest.json", "abc123", "Provision VM")
	if err == nil {
		t.Fatal("runElevatedManifestNative succeeded, want execute timeout")
	}
	if !strings.Contains(err.Error(), "AuthorizationExecuteWithPrivileges wedged after") {
		t.Fatalf("error = %v, want AuthorizationExecuteWithPrivileges timeout", err)
	}
}

func TestRunElevatedManifestNativeRefusesUIThread(t *testing.T) {
	oldTTY := authorizationStdinIsTTY
	oldUIThreadID := uiThreadID.Load()
	t.Cleanup(func() {
		authorizationStdinIsTTY = oldTTY
		uiThreadID.Store(oldUIThreadID)
	})

	authorizationStdinIsTTY = func() bool { return true }
	uiThreadID.Store(currentUIThreadID())

	err := runElevatedManifestNative("/tmp/manifest.json", "abc123", "Provision VM")
	if err == nil {
		t.Fatal("runElevatedManifestNative succeeded on UI thread")
	}
	if !errors.Is(err, ErrAuthorizationOnUIThread) {
		t.Fatalf("error = %v, want ErrAuthorizationOnUIThread", err)
	}
}

func TestPreWarmRunsAuthorizationOffUIThread(t *testing.T) {
	oldAuthInitialized := authInitialized
	oldAuthCreate := authCreate
	oldAuthExecute := authExecute
	oldAuthFree := authFree
	oldTTY := authorizationStdinIsTTY
	oldUIThreadID := uiThreadID.Load()
	t.Cleanup(func() {
		authInitialized = oldAuthInitialized
		authCreate = oldAuthCreate
		authExecute = oldAuthExecute
		authFree = oldAuthFree
		authorizationStdinIsTTY = oldTTY
		uiThreadID.Store(oldUIThreadID)
	})

	authInitialized = true
	authCreate = func(_ uintptr, _ uintptr, _ uint32, authRef *uintptr) int32 {
		if onUIThread() {
			t.Fatal("authCreate ran on UI thread")
		}
		*authRef = 42
		return 0
	}
	authExecute = func(uintptr, uintptr, uint32, uintptr, *uintptr) int32 { return 0 }
	authFree = func(uintptr, uint32) int32 { return 0 }
	authorizationStdinIsTTY = func() bool { return true }
	uiThreadID.Store(currentUIThreadID())

	if err := PreWarm(); err != nil {
		t.Fatalf("PreWarm: %v", err)
	}
}

func stubAuthorizationCreate(t *testing.T, promptVisible bool) func() {
	t.Helper()

	oldAuthInitialized := authInitialized
	oldAuthCreate := authCreate
	oldAuthExecute := authExecute
	oldAuthFree := authFree
	oldNoUITimeout := authCreateNoUITimeout
	oldPromptTimeout := authCreatePromptTimeout
	oldPollInterval := authCreatePollInterval
	oldPromptVisible := authorizationPromptVisible
	oldTTY := authorizationStdinIsTTY

	block := make(chan struct{})
	finished := make(chan struct{})
	authInitialized = true
	authCreate = func(uintptr, uintptr, uint32, *uintptr) int32 {
		defer close(finished)
		<-block
		return 0
	}
	authExecute = func(uintptr, uintptr, uint32, uintptr, *uintptr) int32 { return 0 }
	authFree = func(uintptr, uint32) int32 { return 0 }
	authCreateNoUITimeout = 5 * time.Millisecond
	authCreatePromptTimeout = 5 * time.Millisecond
	authCreatePollInterval = time.Millisecond
	authorizationPromptVisible = func() bool { return promptVisible }
	authorizationStdinIsTTY = func() bool { return true }

	return func() {
		close(block)
		<-finished
		authInitialized = oldAuthInitialized
		authCreate = oldAuthCreate
		authExecute = oldAuthExecute
		authFree = oldAuthFree
		authCreateNoUITimeout = oldNoUITimeout
		authCreatePromptTimeout = oldPromptTimeout
		authCreatePollInterval = oldPollInterval
		authorizationPromptVisible = oldPromptVisible
		authorizationStdinIsTTY = oldTTY
	}
}

func stubAuthorizationExecute(t *testing.T, promptVisible bool) func() {
	t.Helper()

	oldAuthInitialized := authInitialized
	oldAuthCreate := authCreate
	oldAuthExecute := authExecute
	oldAuthFree := authFree
	oldNoUITimeout := authCreateNoUITimeout
	oldPromptTimeout := authCreatePromptTimeout
	oldPollInterval := authCreatePollInterval
	oldPromptVisible := authorizationPromptVisible
	oldTTY := authorizationStdinIsTTY

	block := make(chan struct{})
	finished := make(chan struct{})
	authInitialized = true
	authCreate = func(_ uintptr, _ uintptr, _ uint32, authRef *uintptr) int32 {
		*authRef = 42
		return 0
	}
	authExecute = func(uintptr, uintptr, uint32, uintptr, *uintptr) int32 {
		defer close(finished)
		<-block
		return 0
	}
	authFree = func(uintptr, uint32) int32 { return 0 }
	authCreateNoUITimeout = 5 * time.Millisecond
	authCreatePromptTimeout = 5 * time.Millisecond
	authCreatePollInterval = time.Millisecond
	authorizationPromptVisible = func() bool { return promptVisible }
	authorizationStdinIsTTY = func() bool { return true }

	return func() {
		close(block)
		<-finished
		authInitialized = oldAuthInitialized
		authCreate = oldAuthCreate
		authExecute = oldAuthExecute
		authFree = oldAuthFree
		authCreateNoUITimeout = oldNoUITimeout
		authCreatePromptTimeout = oldPromptTimeout
		authCreatePollInterval = oldPollInterval
		authorizationPromptVisible = oldPromptVisible
		authorizationStdinIsTTY = oldTTY
	}
}
