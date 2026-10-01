package main

import (
	"context"
	"os"
	"testing"
	"time"

	cf "github.com/tmc/apple/corefoundation"
)

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
