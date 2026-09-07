package ios

import _ "embed"

//go:embed research.entitlements
var researchEntitlements []byte

// SigningProfile returns independent copies of the research entitlements and
// required keys. A valid signature does not establish host permission to run it.
func SigningProfile() ([]byte, []string) {
	return append([]byte(nil), researchEntitlements...), []string{
		"com.apple.security.network.server",
		"com.apple.security.network.client",
		"com.apple.security.virtualization",
		"com.apple.private.virtualization",
		"com.apple.private.virtualization.security-research",
	}
}
