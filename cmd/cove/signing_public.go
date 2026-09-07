//go:build darwin && !cove_research

package main

import _ "embed"

//go:embed vz.entitlements
var vzEntitlements []byte

// vzEntitlementKeys are the entitlements cove's binary must carry; all must be
// present for the binary to count as already signed.
var vzEntitlementKeys = []string{
	"com.apple.security.network.server",
	"com.apple.security.network.client",
	"com.apple.security.virtualization",
}

const signingGuardEnv = "_VZ_SIGNED"
