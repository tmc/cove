package controlserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeStatusJSON(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "vnc enabled",
			in: VNCStatus{
				Enabled:     true,
				Port:        5901,
				State:       "listening",
				ServiceName: "cove-default",
				Description: "private console",
			},
			want: `{"enabled":true,"port":5901,"state":"listening","service_name":"cove-default","description":"private console"}`,
		},
		{
			name: "vnc disabled omits empty fields",
			in:   VNCStatus{},
			want: `{"enabled":false}`,
		},
		{
			name: "debug stub listen all",
			in: DebugStubStatus{
				Enabled:     true,
				Kind:        "gdb",
				Port:        1234,
				ListenAll:   true,
				State:       "listening",
				Description: "kernel debug",
			},
			want: `{"enabled":true,"kind":"gdb","port":1234,"listen_all":true,"state":"listening","description":"kernel debug"}`,
		},
		{
			name: "debug stub disabled omits empty fields",
			in:   DebugStubStatus{},
			want: `{"enabled":false}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(data); got != tt.want {
				t.Fatalf("Marshal = %s, want %s", got, tt.want)
			}
			path := filepath.Join(dir, tt.name+".json")
			if err := os.WriteFile(path, data, 0666); err != nil {
				t.Fatal(err)
			}
		})
	}
}
