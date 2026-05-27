package main

import "testing"

func TestAppleAppSandboxContainerIDFromHome(t *testing.T) {
	tests := []struct {
		name string
		home string
		want string
	}{
		{name: "normal home", home: "/Users/tmc", want: ""},
		{name: "container data home", home: "/Users/tmc/Library/Containers/com.tmc.cove/Data", want: "com.tmc.cove"},
		{name: "container subdir ignored", home: "/Users/tmc/Library/Containers/com.tmc.cove/Data/tmp", want: ""},
		{name: "missing data suffix", home: "/Users/tmc/Library/Containers/com.tmc.cove", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appleAppSandboxContainerIDFromHome(tt.home)
			if got != tt.want {
				t.Fatalf("appleAppSandboxContainerIDFromHome(%q) = %q, want %q", tt.home, got, tt.want)
			}
		})
	}
}

func TestCurrentAppleAppSandboxStatusFromHome(t *testing.T) {
	t.Setenv(appleAppSandboxContainerEnv, "")
	t.Setenv("HOME", "/Users/tmc/Library/Containers/com.tmc.cove/Data")
	oldCheck := checkAppleAppSandboxEntitlement
	t.Cleanup(func() { checkAppleAppSandboxEntitlement = oldCheck })
	checkAppleAppSandboxEntitlement = func() bool { return false }

	got := currentAppleAppSandboxStatus()
	if !got.Active || got.ContainerID != "com.tmc.cove" {
		t.Fatalf("currentAppleAppSandboxStatus() = %+v, want active com.tmc.cove", got)
	}
}

func TestCurrentAppleAppSandboxStatusFromEntitlement(t *testing.T) {
	t.Setenv(appleAppSandboxContainerEnv, "")
	t.Setenv("HOME", "/Users/tmc")
	oldCheck := checkAppleAppSandboxEntitlement
	t.Cleanup(func() { checkAppleAppSandboxEntitlement = oldCheck })
	checkAppleAppSandboxEntitlement = func() bool { return true }

	got := currentAppleAppSandboxStatus()
	if !got.Active || got.ContainerID != "" {
		t.Fatalf("currentAppleAppSandboxStatus() = %+v, want active without container id", got)
	}
}
