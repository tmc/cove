package main

import "testing"

func TestWantsRegularUIMode(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		gui           bool
		headless      bool
		legacyRun     bool
		legacyInstall bool
		utmPath       string
		want          bool
	}{
		{name: "default selector", gui: true, want: true},
		{name: "run defaults headed", args: []string{"run"}, gui: true, want: true},
		{name: "install defaults headed", args: []string{"install"}, gui: true, want: true},
		{name: "up defaults headed", args: []string{"up"}, gui: true, want: true},
		{name: "run headless flag after subcommand", args: []string{"run", "-headless"}, gui: true, want: false},
		{name: "up headless flag after subcommand", args: []string{"up", "-headless"}, gui: true, want: false},
		{name: "run gui false after subcommand", args: []string{"run", "-gui=false"}, gui: true, want: false},
		{name: "headless parsed before subcommand", args: []string{"run"}, gui: true, headless: true, want: false},
		{name: "legacy run flag", gui: true, legacyRun: true, want: true},
		{name: "utm bundle defaults headed", gui: true, utmPath: "/tmp/test.utm", want: true},
		{name: "ctl stays background", args: []string{"ctl", "status"}, gui: true, want: false},
		{name: "list stays background", args: []string{"list"}, gui: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wantsRegularUIMode(tt.args, tt.gui, tt.headless, tt.legacyRun, tt.legacyInstall, tt.utmPath)
			if got != tt.want {
				t.Fatalf("wantsRegularUIMode(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestWantsMacgoRuntime(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		legacyRun     bool
		legacyInstall bool
		utmPath       string
		want          bool
	}{
		{name: "default selector", want: true},
		{name: "run command", args: []string{"run"}, want: true},
		{name: "install command", args: []string{"install"}, want: true},
		{name: "up command", args: []string{"up"}, want: true},
		{name: "run headless command", args: []string{"run", "-headless"}, want: true},
		{name: "legacy run flag", legacyRun: true, want: true},
		{name: "legacy install flag", legacyInstall: true, want: true},
		{name: "utm bundle", utmPath: "/tmp/test.utm", want: true},
		{name: "helper command", args: []string{"ctl", "status"}, want: false},
		{name: "list command", args: []string{"list"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wantsMacgoRuntime(tt.args, tt.legacyRun, tt.legacyInstall, tt.utmPath)
			if got != tt.want {
				t.Fatalf("wantsMacgoRuntime(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDesiredMacgoUIMode(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		gui           bool
		headless      bool
		legacyRun     bool
		legacyInstall bool
		utmPath       string
		want          string
	}{
		{name: "headed run uses regular", args: []string{"run"}, gui: true, want: "regular"},
		{name: "headless run uses accessory", args: []string{"run"}, gui: true, headless: true, want: "accessory"},
		{name: "explicit headless flag uses accessory", args: []string{"run", "-headless"}, gui: true, want: "accessory"},
		{name: "gui false uses accessory", args: []string{"run", "-gui=false"}, gui: true, want: "accessory"},
		{name: "legacy headed install uses regular", gui: true, legacyInstall: true, want: "regular"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desiredMacgoUIMode(tt.args, tt.gui, tt.headless, tt.legacyRun, tt.legacyInstall, tt.utmPath)
			if string(got) != tt.want {
				t.Fatalf("desiredMacgoUIMode(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestShouldEnableMacgo(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		args []string
		gui  bool
		want bool
	}{
		{
			name: "disabled by default for headed runtime command",
			args: []string{"up"},
			gui:  true,
			want: false,
		},
		{
			name: "disabled by default for headless runtime command",
			args: []string{"run", "-headless"},
			gui:  true,
			want: false,
		},
		{
			name: "disabled explicitly",
			env:  map[string]string{"VZMAC_NO_MACGO": "1"},
			args: []string{"up"},
			gui:  true,
			want: false,
		},
		{
			name: "non-runtime command stays off by default",
			args: []string{"ctl", "status"},
			gui:  true,
			want: false,
		},
		{
			name: "headed runtime command can still be opted in",
			env: map[string]string{
				vzmacEnableMacgoEnv: "1",
			},
			args: []string{"up"},
			gui:  true,
			want: true,
		},
		{
			name: "helper command stays off even when opted in",
			env: map[string]string{
				vzmacEnableMacgoEnv: "1",
			},
			args: []string{"ctl", "status"},
			gui:  true,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			got := shouldEnableMacgo(tt.args, tt.gui, false, false, false, "")
			if got != tt.want {
				t.Fatalf("shouldEnableMacgo(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
