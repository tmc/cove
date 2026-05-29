package main

import (
	"reflect"
	"testing"
)

func TestMoveSoftresetProbeFlagsFirst(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"alreadyFirst", []string{"--all", "vm"}, []string{"--all", "vm"}},
		{"flagAfterVM", []string{"vm", "--all"}, []string{"--all", "vm"}},
		{"probesEqualsForm", []string{"vm", "--probes=fs,net"}, []string{"--probes=fs,net", "vm"}},
		{"probesPairForm", []string{"vm", "--probes", "fs,net"}, []string{"--probes", "fs,net", "vm"}},
		{"probesAtEndOnly", []string{"--probes"}, []string{"--probes"}},
		{"empty", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := moveSoftresetProbeFlagsFirst(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got = %v, want %v", got, tt.want)
			}
		})
	}
}
