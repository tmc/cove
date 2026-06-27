//go:build darwin

package main

import (
	_ "embed"

	"github.com/tmc/apple/x/codesign"
)

//go:embed vz.entitlements
var vzEntitlements []byte

// vzEntitlementKeys are the entitlements cove's binary must carry; all must be
// present for the binary to count as already signed.
var vzEntitlementKeys = []string{
	"com.apple.security.network.server",
	"com.apple.security.network.client",
	"com.apple.security.virtualization",
}

// ensureEntitlements ad-hoc signs the running binary with the virtualization
// and network entitlements if any is missing, then re-execs. It delegates to
// github.com/tmc/apple/x/codesign; cove keeps only its embedded entitlements
// plist and re-exec guard.
func ensureEntitlements() error {
	return codesign.EnsureSigned(codesign.Options{
		Entitlements: vzEntitlements,
		RequireKeys:  vzEntitlementKeys,
		GuardEnv:     "_VZ_SIGNED",
	})
}
