package agent

import (
	"strconv"
	"strings"
)

type VersionRelation int

// ProtocolVersion is the cache-facing guest-agent protocol version.
const ProtocolVersion = "1"

const (
	VersionUnknown VersionRelation = iota
	VersionEqual
	VersionGuestOlder
	VersionGuestNewer
	VersionDifferent
)

func CompareVersions(host, guest string) VersionRelation {
	if host == "" || host == "dev" || host == "unknown" {
		return VersionUnknown
	}
	if guest == "" || guest == "dev" || guest == "unknown" {
		return VersionUnknown
	}
	if host == guest {
		return VersionEqual
	}
	hostParts, hostOK := parseSemver(host)
	guestParts, guestOK := parseSemver(guest)
	if hostOK && guestOK {
		switch {
		case semverLess(guestParts, hostParts):
			return VersionGuestOlder
		case semverLess(hostParts, guestParts):
			return VersionGuestNewer
		default:
			return VersionEqual
		}
	}
	return VersionDifferent
}

func VersionsEqual(host, guest string) bool {
	return CompareVersions(host, guest) == VersionEqual
}

func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func semverLess(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
