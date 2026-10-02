package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func TestCtlDisplayHelp(t *testing.T) {
	for _, tt := range []struct {
		name  string
		print func(*bytes.Buffer)
		want  []string
	}{
		{"commands", func(w *bytes.Buffer) { printCtlUsage(w, flag.NewFlagSet("ctl", flag.ContinueOnError)) }, []string{"configured display geometry", "live metrics unavailable"}},
		{"display", func(w *bytes.Buffer) { printCtlSubcommandUsage(w, "display", nil) }, []string{"configuration_source=run_config", "not the guest's current mode", "does\nnot infer default geometry", "available=false", "live_metrics_available=false"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			tt.print(&out)
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("help missing %q:\n%s", want, &out)
				}
			}
			if strings.Contains(out.String(), "read from the live paravirtualized display") {
				t.Fatal("help claims live private observation")
			}
		})
	}
}
