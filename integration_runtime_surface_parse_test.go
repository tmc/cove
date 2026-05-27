//go:build integration && darwin && arm64

package main

import "testing"

func TestRuntimeSurfaceMacOSDiskStateParsing(t *testing.T) {
	output := `
== diskutil-info-root ==
   Container Total Space:     63.9 GB (63888117760 Bytes)
== diskutil-info-disk0 ==
   Disk Size:                 137.4 GB (137438953472 Bytes) (exactly 268435456 512-Byte-Units)
== df-root ==
Filesystem     1024-blocks     Used Available Capacity iused ifree %iused Mounted on
/dev/disk3s5s1    62390740 20800000  41590740    34%  502000 4.1G    1%   /
`
	if got := parseDiskutilByteLine(t, runtimeSurfaceOutputSection(output, "== diskutil-info-disk0 =="), "Disk Size:"); got != 137438953472 {
		t.Fatalf("physical disk bytes = %d, want %d", got, uint64(137438953472))
	}
	if got := parseDiskutilByteLine(t, runtimeSurfaceOutputSection(output, "== diskutil-info-root =="), "Container Total Space:"); got != 63888117760 {
		t.Fatalf("container bytes = %d, want %d", got, uint64(63888117760))
	}
	if got := parseDFRootKBytes(t, output); got != 62390740 {
		t.Fatalf("df root kbytes = %d, want %d", got, uint64(62390740))
	}
}

func TestIntegrationArtifactName(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{in: "TestIntegration/runtime-surface/disk-resize-live", want: "TestIntegration-runtime-surface-disk-resize-live"},
		{in: "///", want: "integration"},
	} {
		if got := integrationArtifactName(tt.in); got != tt.want {
			t.Fatalf("integrationArtifactName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
