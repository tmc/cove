package main

import (
	"testing"
	"time"
)

func TestEmitImageGCKeepUnknownReasonIsNoOp(t *testing.T) {
	emitImageGCKeep(ImageRef{}, "frobnicate", time.Now())
}

func TestEmitImageGCKeepInUseDoesNotPanic(t *testing.T) {
	emitImageGCKeep(ImageRef{Name: "demo", Tag: "v1"}, "in_use", time.Now())
}

func TestEmitImageGCKeepRecentDoesNotPanic(t *testing.T) {
	emitImageGCKeep(ImageRef{Name: "demo", Tag: "v1"}, "recent", time.Now())
}
