//go:build darwin

package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestSigningProfile(t *testing.T) {
	d := xml.NewDecoder(bytes.NewReader(vzEntitlements))
	keys := make(map[string]bool)
	key := ""
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			if err := d.DecodeElement(&key, &start); err != nil {
				t.Fatal(err)
			}
		case "true", "false":
			if _, ok := keys[key]; ok {
				t.Fatalf("duplicate entitlement %q", key)
			}
			keys[key] = start.Name.Local == "true"
		}
	}
	if len(keys) != len(vzEntitlementKeys) {
		t.Fatalf("plist and required keys differ: %v / %v", keys, vzEntitlementKeys)
	}
	for _, key := range vzEntitlementKeys {
		if !keys[key] {
			t.Errorf("required entitlement %q is not true", key)
		}
	}
	for _, key := range []string{"com.apple.security.virtualization", "com.apple.security.network.client", "com.apple.security.network.server"} {
		if !keys[key] {
			t.Errorf("missing public entitlement %s", key)
		}
	}
	research := signingGuardEnv == "_COVE_RESEARCH_SIGNED"
	for _, key := range []string{"com.apple.private.virtualization", "com.apple.private.virtualization.security-research"} {
		if keys[key] != research {
			t.Errorf("research entitlement %s = %v, research build %v", key, keys[key], research)
		}
	}
	if !research {
		for key := range keys {
			if strings.HasPrefix(key, "com.apple.private.") {
				t.Errorf("private entitlement in public build: %s", key)
			}
		}
	}
}
