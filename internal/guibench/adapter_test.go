package guibench

import "testing"

func TestParseProvenance(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		want    Provenance
		wantErr bool
	}{
		{
			name:   "port with note",
			source: "adapted:osworld:030eeff7-b492-4218-b312-701ec99ee0cc mode=port; Chrome DNT toggle maps to Safari defaults",
			want: Provenance{
				Benchmark:  "osworld",
				UpstreamID: "030eeff7-b492-4218-b312-701ec99ee0cc",
				Mode:       ModePort,
				Note:       "Chrome DNT toggle maps to Safari defaults",
			},
		},
		{
			name:   "intent uppercase benchmark folds",
			source: "adapted:OSWorld:01b269ae mode=intent; LibreOffice Calc fill-down to Numbers",
			want: Provenance{
				Benchmark:  "osworld",
				UpstreamID: "01b269ae",
				Mode:       ModeIntent,
				Note:       "LibreOffice Calc fill-down to Numbers",
			},
		},
		{
			name:   "webarena numeric id",
			source: "adapted:webarena:101 mode=port; navigate a tab",
			want: Provenance{
				Benchmark:  "webarena",
				UpstreamID: "101",
				Mode:       ModePort,
				Note:       "navigate a tab",
			},
		},
		{
			name:   "path-style upstream id",
			source: "adapted:cua-bench:apps/reminders.py mode=intent; native Reminders getter",
			want: Provenance{
				Benchmark:  "cua-bench",
				UpstreamID: "apps/reminders.py",
				Mode:       ModeIntent,
				Note:       "native Reminders getter",
			},
		},
		{name: "not adapted", source: "cove-original (design 047)", wantErr: true},
		{name: "missing mode", source: "adapted:osworld:abc; note", wantErr: true},
		{name: "unknown mode", source: "adapted:osworld:abc mode=verbatim; note", wantErr: true},
		{name: "missing upstream id", source: "adapted:osworld mode=port; note", wantErr: true},
		{name: "empty benchmark", source: "adapted::abc mode=port; note", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProvenance(tt.source)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseProvenance(%q) = %+v, want error", tt.source, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProvenance(%q): %v", tt.source, err)
			}
			if got != tt.want {
				t.Errorf("parseProvenance(%q) = %+v, want %+v", tt.source, got, tt.want)
			}
		})
	}
}

func TestTaskIsAdapted(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"adapted:osworld:abc mode=port; note", true},
		{"  adapted:osworld:abc mode=port; note", true},
		{"cove-original (design 047 §9)", false},
		{"", false},
	}
	for _, tt := range tests {
		task := &Task{Source: tt.source}
		if got := task.IsAdapted(); got != tt.want {
			t.Errorf("IsAdapted(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}
