//go:build !darwin && !linux

package main

import "context"

func populateProcessInfo(_ context.Context, _ *systemInfo) {}
