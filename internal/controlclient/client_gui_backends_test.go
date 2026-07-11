package controlclient

import "testing"

func TestParseGUIBackends(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		wantCapture string
		wantInput   string
		wantErr     bool
	}{
		{
			name:        "window input",
			data:        `{"capture_backend":"auto","input_backend":"window"}`,
			wantCapture: "auto",
			wantInput:   "window",
		},
		{
			name:        "framebuffer",
			data:        `{"capture_backend":"framebuffer","input_backend":"direct","headed":false}`,
			wantCapture: "framebuffer",
			wantInput:   "direct",
		},
		{name: "missing fields", data: `{"headed":true}`, wantErr: true},
		{name: "invalid json", data: `not json`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture, input, err := ParseGUIBackends(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseGUIBackends(%q) = %q, %q, nil; want error", tt.data, capture, input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseGUIBackends(%q): %v", tt.data, err)
			}
			if capture != tt.wantCapture || input != tt.wantInput {
				t.Fatalf("ParseGUIBackends(%q) = %q, %q; want %q, %q", tt.data, capture, input, tt.wantCapture, tt.wantInput)
			}
		})
	}
}
