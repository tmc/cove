//go:build !darwin

package main

func appleAppSandboxEntitlement() bool {
	return false
}
