package covecli

import "testing"

func TestDispatchName(t *testing.T) {
	tests := []struct {
		name string
		in   Dispatch
		want string
	}{
		{"pre ui", DispatchPreUI, "pre-ui"},
		{"early", DispatchEarly, "early"},
		{"late", DispatchLate, "late"},
		{"unknown", Dispatch(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DispatchName(tt.in); got != tt.want {
				t.Fatalf("DispatchName(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCommandMetadata(t *testing.T) {
	tests := []struct {
		name              string
		mutatesState      bool
		requiresRunningVM bool
		mayBootVM         bool
		safeForDiscovery  bool
	}{
		{"commands", false, false, false, true},
		{"run", true, false, true, false},
		{"status", false, true, false, false},
		{"image", true, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MutatesState(tt.name); got != tt.mutatesState {
				t.Fatalf("MutatesState(%q) = %v, want %v", tt.name, got, tt.mutatesState)
			}
			if got := RequiresRunningVM(tt.name); got != tt.requiresRunningVM {
				t.Fatalf("RequiresRunningVM(%q) = %v, want %v", tt.name, got, tt.requiresRunningVM)
			}
			if got := MayBootVM(tt.name); got != tt.mayBootVM {
				t.Fatalf("MayBootVM(%q) = %v, want %v", tt.name, got, tt.mayBootVM)
			}
			if got := SafeForDiscovery(tt.name); got != tt.safeForDiscovery {
				t.Fatalf("SafeForDiscovery(%q) = %v, want %v", tt.name, got, tt.safeForDiscovery)
			}
		})
	}
}

func TestOutputHints(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{"commands", []string{"text", "json"}},
		{"ctl", []string{"text", "json", "binary"}},
		{"serve", []string{"text", "http", "mcp"}},
		{"run", []string{"text"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OutputHints(tt.name)
			if len(got) != len(tt.want) {
				t.Fatalf("OutputHints(%q) = %v, want %v", tt.name, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("OutputHints(%q) = %v, want %v", tt.name, got, tt.want)
				}
			}
		})
	}
}
