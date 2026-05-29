package main

import "testing"

func TestAllowHIDKeyboard(t *testing.T) {
	tests := []struct {
		name        string
		legacy      string
		disable     string
		wantAllow   bool
		wantDisable bool
	}{
		{
			name:      "unset",
			wantAllow: true,
		},
		{
			name:      "legacy enable",
			legacy:    "1",
			wantAllow: true,
		},
		{
			name:        "legacy disable",
			legacy:      "0",
			wantDisable: true,
		},
		{
			name:        "new disable",
			disable:     "1",
			wantDisable: true,
		},
		{
			name:      "new false",
			disable:   "false",
			wantAllow: true,
		},
		{
			name:        "explicit disable wins",
			legacy:      "1",
			disable:     "1",
			wantDisable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD", tt.legacy)
			t.Setenv("VZ_MACOS_DISABLE_HID_KEYBOARD", tt.disable)

			if got := allowHIDKeyboard(); got != tt.wantAllow {
				t.Fatalf("allowHIDKeyboard() = %v, want %v", got, tt.wantAllow)
			}
			if got := disableHIDKeyboardOptOut(); got != tt.wantDisable {
				t.Fatalf("disableHIDKeyboardOptOut() = %v, want %v", got, tt.wantDisable)
			}
		})
	}
}
