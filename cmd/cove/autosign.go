//go:build darwin

package main

import (
	"github.com/tmc/apple/x/codesign"
)

// ensureEntitlements ad-hoc signs the running binary with the virtualization
// and network entitlements if any is missing, then re-execs. It delegates to
// github.com/tmc/apple/x/codesign; cove keeps only its embedded entitlements
// plist and re-exec guard.
func ensureEntitlements() error {
	return codesign.EnsureSigned(codesign.Options{
		Entitlements: vzEntitlements,
		RequireKeys:  vzEntitlementKeys,
		GuardEnv:     signingGuardEnv,
	})
}
