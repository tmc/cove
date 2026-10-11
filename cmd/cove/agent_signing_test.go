package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAgentSigningArguments(t *testing.T) {
	cmd := agentSigningCommand("/agent path/vz-agent", "Agent Signing Identity")
	want := []string{"codesign", "-s", "Agent Signing Identity", "-f", "--identifier", "com.tmc.cove.vz-agent", "/agent path/vz-agent"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
}

func TestAgentSigningSelection(t *testing.T) {
	for _, tt := range []struct {
		name, identity string
		fail           bool
		want           string
	}{
		{"default", "", false, "-s\n-\n-f\n/agent path/vz-agent\n"},
		{"selected", "Agent Identity", false, "-s\nAgent Identity\n-f\n--identifier\ncom.tmc.cove.vz-agent\n/agent path/vz-agent\n"},
		{"selected failure", "Missing Identity", true, "-s\nMissing Identity\n-f\n--identifier\ncom.tmc.cove.vz-agent\n/agent path/vz-agent\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "args")
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$SIGNING_TEST_LOG\"\n"
			if tt.fail {
				script += "exit 17\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "codesign"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SIGNING_TEST_LOG", log)
			t.Setenv("COVE_AGENT_SIGN_IDENTITY", tt.identity)
			err := signAgentBinary("/agent path/vz-agent")
			if tt.fail {
				if err == nil || !strings.Contains(err.Error(), "Missing Identity") || !strings.Contains(err.Error(), "exit status 17") {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("invocations = %q, want %q (no fallback)", got, tt.want)
			}
		})
	}
}

func TestAgentBuildSigningFailure(t *testing.T) {
	for _, targetOS := range []string{"darwin", "linux", "windows"} {
		t.Run(targetOS, func(t *testing.T) {
			dir := t.TempDir()
			module, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			module = filepath.Dir(filepath.Dir(module))
			log := filepath.Join(dir, "calls")
			for name, script := range map[string]string{
				"go":       "#!/bin/sh\nprintf 'build\\n' >> \"$SIGNING_TEST_LOG\"\n",
				"codesign": "#!/bin/sh\nprintf 'sign\\n' >> \"$SIGNING_TEST_LOG\"\nexit 17\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("COVE_SRC", module)
			t.Setenv("SIGNING_TEST_LOG", log)
			t.Setenv("COVE_AGENT_SIGN_IDENTITY", "Missing Identity")
			err = buildAgentBinaryForOS(filepath.Join(dir, "vz-agent"), targetOS)
			want := "build\n"
			if targetOS == "darwin" {
				want += "sign\n"
				if err == nil || !strings.Contains(err.Error(), "codesign agent binary: sign agent with identity") {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			got, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != want {
				t.Fatalf("calls = %q, want %q", got, want)
			}
		})
	}
}
