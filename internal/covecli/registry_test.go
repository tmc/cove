package covecli

import "testing"

func TestLookup(t *testing.T) {
	registry := []Spec[int]{
		{Name: "run", Dispatch: DispatchLate},
		{Name: "list", Aliases: []string{"ls"}, Dispatch: DispatchLate},
	}
	tests := []struct {
		name     string
		wantName string
		wantOK   bool
	}{
		{"run", "run", true},
		{"ls", "list", true},
		{"missing", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(registry, tt.name)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Name != tt.wantName {
				t.Fatalf("Lookup(%q) name = %q, want %q", tt.name, got.Name, tt.wantName)
			}
		})
	}
}

func TestNames(t *testing.T) {
	registry := []Spec[int]{
		{Name: "run"},
		{Name: "list", Aliases: []string{"ls"}},
	}
	got := Names(registry)
	want := []string{"run", "list", "ls", "help"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}
