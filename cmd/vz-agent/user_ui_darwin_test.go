package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	cf "github.com/tmc/apple/corefoundation"
)

func TestAXAttributeFailureContext(t *testing.T) {
	backend := &darwinUIBackend{
		timeout:       func(uintptr, float32) int32 { return 0 },
		copyAttribute: func(uintptr, uintptr, *uintptr) int32 { return -25200 },
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := backend.attribute(ctx, 1, "AXIdentifier")
	var failure *uiReadError
	if !errors.As(err, &failure) || failure.state != "read_failed" || !strings.Contains(err.Error(), "AXIdentifier") {
		t.Fatalf("attribute error = %v", err)
	}
}

func TestPublicAXTimeoutFloatABI(t *testing.T) {
	backend := &darwinUIBackend{}
	backend.load()
	if backend.err != nil {
		t.Fatal(backend.err)
	}
	element := backend.create(int32(os.Getpid()))
	if element == 0 {
		t.Fatal("create own application accessibility reference")
	}
	defer cf.CFRelease(cf.CFTypeRef(element))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := backend.prepare(ctx, element); err != nil {
		t.Fatalf("native float timeout ABI: %v", err)
	}
}
