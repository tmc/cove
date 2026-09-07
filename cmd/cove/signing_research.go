//go:build darwin && cove_research

package main

import "github.com/tmc/cove/internal/ios"

var vzEntitlements, vzEntitlementKeys = ios.SigningProfile()

const signingGuardEnv = "_COVE_RESEARCH_SIGNED"
