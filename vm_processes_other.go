//go:build !darwin

package main

func defaultVMProcessCollector() vmProcessCollector {
	return commandVMProcessCollector{runner: execVMProcessRunner{}}
}

func openFileHolderPIDs(string) ([]int, error) {
	return nil, nil
}
