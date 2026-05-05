package main

import (
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
	}
}
