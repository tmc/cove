package main

import "testing"

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "testuser", false},
		{"valid with underscore", "test_user", false},
		{"valid with numbers", "user123", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 256)), true},
		{"reserved root", "root", true},
		{"reserved daemon", "daemon", true},
		{"reserved nobody", "nobody", true},
		{"reserved wheel", "wheel", true},
		{"reserved admin", "admin", true},
		{"reserved staff", "staff", true},
		{"reserved case insensitive", "Root", true},
		{"invalid slash", "user/name", true},
		{"invalid backslash", "user\\name", true},
		{"invalid colon", "user:name", true},
		{"invalid star", "user*name", true},
		{"invalid question", "user?name", true},
		{"invalid quote", "user\"name", true},
		{"invalid lt", "user<name", true},
		{"invalid gt", "user>name", true},
		{"invalid pipe", "user|name", true},
		{"invalid newline", "user\nname", true},
		{"invalid tab", "user\tname", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUsername(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateUsername(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
