package softreset

import (
	"path/filepath"
	"testing"
)

func TestCheckScratchRootRejectsUnsafe(t *testing.T) {
	cases := []string{
		".",
		string(filepath.Separator),
		"/tmp/scratch", // no softreset in basename
		"/var/empty",
	}
	for _, root := range cases {
		if err := checkScratchRoot(root); err == nil {
			t.Errorf("checkScratchRoot(%q) = nil, want error", root)
		}
	}
}

func TestCheckScratchRootAcceptsSoftresetBasename(t *testing.T) {
	for _, root := range []string{"/tmp/softreset-x", "/tmp/foo/soft-reset-y"} {
		if err := checkScratchRoot(root); err != nil {
			t.Errorf("checkScratchRoot(%q) = %v, want nil", root, err)
		}
	}
}

