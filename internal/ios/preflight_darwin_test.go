//go:build darwin

package ios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseEntitlements(t *testing.T) {
	tests := []struct {
		name, xml string
		want      bool
		bad       bool
	}{
		{"comments", `<plist><dict><!-- before --><?audit check?><key>required</key><!-- between --><true/></dict></plist>`, true, false},
		{"true", `<plist><dict><key>required</key><true/></dict></plist>`, true, false},
		{"false", `<plist><dict><key>required</key><false/></dict></plist>`, false, false},
		{"string", `<plist><dict><key>required</key><string>true</string></dict></plist>`, false, false},
		{"nested key", `<plist><dict><key>nested</key><dict><key>required</key><true/></dict></dict></plist>`, false, false},
		{"duplicate", `<plist><dict><key>required</key><false/><key>required</key><true/></dict></plist>`, false, true},
		{"no dictionary", `<plist/>`, false, true},
		{"missing value", `<plist><dict><key>required</key></dict></plist>`, false, true},
		{"malformed", `<plist><dict>`, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEntitlements([]byte(tt.xml))
			if (err != nil) != tt.bad {
				t.Fatalf("parse = %v, want error %v", err, tt.bad)
			}
			if !tt.bad && got["required"] != tt.want {
				t.Fatalf("required = %v, want %v", got["required"], tt.want)
			}
		})
	}
}

func ExampleInspectHost_cancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := InspectHost(ctx, "/path/to/cove-research")
	fmt.Println(report.SignatureValid)
	// Output: false
}

func TestInspectHostEntitlements(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "--verify" ]; then exit 0; fi
/bin/cat <<'PLIST'
<plist><dict>
<key>com.apple.security.network.server</key><true/>
<key>com.apple.security.network.client</key><true/>
<key>com.apple.security.virtualization</key><true/>
<key>com.apple.private.virtualization</key><false/>
</dict></plist>
PLIST
`
	if err := os.WriteFile(filepath.Join(dir, "codesign"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	report := InspectHost(context.Background(), "fixture")
	if !reflect.DeepEqual(report.MissingEntitlements, []string{"com.apple.private.virtualization.security-research"}) {
		t.Fatalf("missing = %v", report.MissingEntitlements)
	}
	if !reflect.DeepEqual(report.InactiveEntitlements, []string{"com.apple.private.virtualization"}) {
		t.Fatalf("inactive = %v", report.InactiveEntitlements)
	}
}
