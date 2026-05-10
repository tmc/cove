package agent

import "testing"

func TestCompareVersions(t *testing.T) {
	if ProtocolVersion == "" {
		t.Fatal("ProtocolVersion is empty")
	}
	tests := []struct {
		name  string
		host  string
		guest string
		want  VersionRelation
	}{
		{"identical release", "v0.2.3", "v0.2.3", VersionEqual},
		{"identical commit", "abc12345", "abc12345", VersionEqual},
		{"guest older patch", "v0.2.3", "v0.2.2", VersionGuestOlder},
		{"guest older minor", "v0.3.0", "v0.2.9", VersionGuestOlder},
		{"guest older major", "v1.0.0", "v0.9.9", VersionGuestOlder},
		{"guest newer patch", "v0.2.2", "v0.2.3", VersionGuestNewer},
		{"guest newer minor", "v0.2.3", "v0.3.0", VersionGuestNewer},
		{"guest newer major", "v0.9.9", "v1.0.0", VersionGuestNewer},
		{"semver without v prefix", "0.2.2", "0.2.3", VersionGuestNewer},
		{"semver with prerelease equal majmin", "v0.2.3-rc1", "v0.2.3-rc2", VersionEqual},
		{"two distinct commits", "abc12345", "def67890", VersionDifferent},
		{"semver vs commit", "v0.2.3", "abc12345", VersionDifferent},
		{"host empty", "", "v0.2.3", VersionUnknown},
		{"guest empty", "v0.2.3", "", VersionUnknown},
		{"both empty", "", "", VersionUnknown},
		{"host dev", "dev", "v0.2.3", VersionUnknown},
		{"guest dev", "v0.2.3", "dev", VersionUnknown},
		{"both dev", "dev", "dev", VersionUnknown},
		{"host unknown", "unknown", "v0.2.3", VersionUnknown},
		{"guest unknown", "v0.2.3", "unknown", VersionUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompareVersions(tt.host, tt.guest); got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.host, tt.guest, got, tt.want)
			}
		})
	}
}

func TestVersionsEqual(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		guest string
		want  bool
	}{
		{"identical release", "v0.2.3", "v0.2.3", true},
		{"identical commit", "abc12345", "abc12345", true},
		{"mismatch release", "v0.2.3", "v0.2.4", false},
		{"mismatch commit", "abc12345", "def67890", false},
		{"host empty", "", "v0.2.3", false},
		{"guest empty", "v0.2.3", "", false},
		{"both empty", "", "", false},
		{"host dev", "dev", "v0.2.3", false},
		{"guest dev", "v0.2.3", "dev", false},
		{"both dev", "dev", "dev", false},
		{"host unknown", "unknown", "v0.2.3", false},
		{"guest unknown", "v0.2.3", "unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VersionsEqual(tt.host, tt.guest); got != tt.want {
				t.Errorf("VersionsEqual(%q, %q) = %v, want %v", tt.host, tt.guest, got, tt.want)
			}
		})
	}
}

func TestVersionRelationString(t *testing.T) {
	for _, tt := range []struct {
		in   VersionRelation
		want string
	}{
		{VersionUnknown, "unknown"},
		{VersionEqual, "equal"},
		{VersionGuestOlder, "guest-older"},
		{VersionGuestNewer, "guest-newer"},
		{VersionDifferent, "different"},
		{VersionRelation(99), "invalid"},
	} {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("VersionRelation(%d).String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}
