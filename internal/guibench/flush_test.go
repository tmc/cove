package guibench

import "testing"

func TestFlush(t *testing.T) {
	tests := []struct {
		name     string
		kind     FlushKind
		path     string
		commands map[string]ExecResult
		wantErr  bool
	}{
		{
			name:     "cfprefsd ok",
			kind:     FlushCfprefsd,
			commands: map[string]ExecResult{"killall cfprefsd": {ExitCode: 0}},
		},
		{
			name:     "cfprefsd no process is benign",
			kind:     FlushCfprefsd,
			commands: map[string]ExecResult{"killall cfprefsd": {ExitCode: 1, Stderr: "No matching processes belonging to you were found"}},
		},
		{
			name:     "cfprefsd real failure errors",
			kind:     FlushCfprefsd,
			commands: map[string]ExecResult{"killall cfprefsd": {ExitCode: 1, Stderr: "Operation not permitted"}},
			wantErr:  true,
		},
		{
			name:     "wal checkpoint ok",
			kind:     FlushWAL,
			path:     "/tmp/x.db",
			commands: map[string]ExecResult{"sqlite3 /tmp/x.db PRAGMA wal_checkpoint(FULL);": {ExitCode: 0, Stdout: "0|1|1\n"}},
		},
		{
			name:     "wal checkpoint failure errors",
			kind:     FlushWAL,
			path:     "/tmp/x.db",
			commands: map[string]ExecResult{"sqlite3 /tmp/x.db PRAGMA wal_checkpoint(FULL);": {ExitCode: 1, Stderr: "database is locked"}},
			wantErr:  true,
		},
		{
			name:    "wal needs path",
			kind:    FlushWAL,
			path:    "",
			wantErr: true,
		},
		{
			name:    "unknown kind",
			kind:    "spotlight",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := FakeProbe{Commands: tt.commands}
			err := Flush(probe, tt.kind, tt.path)
			if tt.wantErr && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
