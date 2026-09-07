package ios

import (
	"bytes"
	"fmt"
	"testing"
)

func ExampleSigningProfile() {
	plist, keys := SigningProfile()
	fmt.Println(len(plist) > 0)
	fmt.Println(keys[len(keys)-1])
	// Output:
	// true
	// com.apple.private.virtualization.security-research
}

func TestSigningProfileCopies(t *testing.T) {
	plist, keys := SigningProfile()
	want := append([]byte(nil), plist...)
	plist[0] = 0
	keys[0] = "changed"
	got, gotKeys := SigningProfile()
	if !bytes.Equal(got, want) || gotKeys[0] == "changed" {
		t.Fatal("caller changed shared signing profile")
	}
}
