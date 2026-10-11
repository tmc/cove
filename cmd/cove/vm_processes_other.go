//go:build !darwin

package main

import (
	"context"
	"fmt"
)

func defaultVMProcessCollector() vmProcessCollector {
	return commandVMProcessCollector{runner: execVMProcessRunner{}}
}

func openFileHolderPIDs(string) ([]int, error) {
	return nil, nil
}

func openFileHolderPIDsContext(context.Context, string) ([]int, error) {
	return nil, fmt.Errorf("file holder observation unsupported; retained")
}
