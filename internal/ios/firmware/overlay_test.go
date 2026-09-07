//go:build darwin || linux

package firmware

import "testing"

func TestPatchMountSourceRejectsDrift(t *testing.T) {
	if _, err := patchMountSource("// changed upstream source\n"); err == nil {
		t.Fatal("accepted changed upstream source")
	}
}
