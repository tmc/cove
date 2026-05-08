package sckit

// Probe is a snapshot of the host's ScreenCaptureKit readiness.
//
// SCKitAvailable is true when the SCStream Objective-C class can be
// resolved at runtime and the host macOS version meets the SCKit
// floor (14.0). ScreenRecordingAuthorized reflects the result of
// CGPreflightScreenCaptureAccess, a read-only check that never
// prompts the user. MacOSVersion is the dotted product version
// reported by sw_vers, or empty when it could not be read.
type Probe struct {
	SCKitAvailable            bool
	ScreenRecordingAuthorized bool
	MacOSVersion              string
}

// macOSVersionAtLeast reports whether got is >= want using dotted
// numeric comparison. Non-numeric components compare as zero. An
// empty got returns false.
func macOSVersionAtLeast(got string, wantMajor, wantMinor int) bool {
	if got == "" {
		return false
	}
	major, minor := parseDottedVersion(got)
	if major != wantMajor {
		return major > wantMajor
	}
	return minor >= wantMinor
}

func parseDottedVersion(s string) (int, int) {
	major, minor := 0, 0
	dot := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		major = atoiSafe(s)
		return major, 0
	}
	major = atoiSafe(s[:dot])
	rest := s[dot+1:]
	dot2 := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '.' {
			dot2 = i
			break
		}
	}
	minor = atoiSafe(rest[:dot2])
	return major, minor
}

func atoiSafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
