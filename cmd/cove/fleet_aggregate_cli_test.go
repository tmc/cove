package main

import (
	"bytes"
	"testing"
)

func TestPrintFleetAggregate(t *testing.T) {
	tests := []struct {
		name string
		rows []fleetAggregateRow
		want string
	}{
		{
			name: "empty",
			rows: nil,
			want: "no fleet remotes\n",
		},
		{
			name: "error row",
			rows: []fleetAggregateRow{{Host: "h1", Error: "dial: refused"}},
			want: "h1\t(unreachable)\tdial: refused\n",
		},
		{
			name: "blank output",
			rows: []fleetAggregateRow{{Host: "h2", Output: "   "}},
			want: "h2\t(no results)\n",
		},
		{
			name: "multiline output",
			rows: []fleetAggregateRow{{Host: "h3", Output: "vm-a\nvm-b"}},
			want: "h3\tvm-a\nh3\tvm-b\n",
		},
		{
			name: "mixed",
			rows: []fleetAggregateRow{
				{Host: "h1", Error: "boom"},
				{Host: "h2", Output: ""},
				{Host: "h3", Output: "ok"},
			},
			want: "h1\t(unreachable)\tboom\nh2\t(no results)\nh3\tok\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := printFleetAggregate(&buf, tt.rows); err != nil {
				t.Fatalf("printFleetAggregate: %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}
