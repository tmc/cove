package main

import "testing"

func TestIOSProbeSelectorEncoding(t *testing.T) {
	for _, tt := range []struct {
		name      string
		available bool
		encoding  string
		want      string
		valid     bool
	}{
		{"uint32", true, "v20@0:8I16", "v@:I", true},
		{"int64", true, "v24@0:8q16", "v@:q", true},
		{"ecid", true, "Q16@0:8", "Q@:", true},
		{"object instead of scalar", true, "v24@0:8@16", "v@:I", false},
		{"wrong scalar width", true, "v24@0:8Q16", "v@:I", false},
		{"missing", false, "", "v@:I", false},
		{"missing encoding", true, "", "v@:I", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIOSProbeSelector(iosSelectorProbe{Class: "test", Selector: "setValue:", Available: tt.available, Encoding: tt.encoding, ExpectedEncoding: tt.want})
			if (err == nil) != tt.valid {
				t.Fatalf("validate = %v, want valid %v", err, tt.valid)
			}
		})
	}
}
