package agentsandbox

import (
	"context"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	sb, err := New("OpenAI", "agentkit/macos-base:latest")
	if err != nil {
		t.Fatal(err)
	}
	if sb.Provider != "openai" || sb.Image != "agentkit/macos-base:latest" {
		t.Fatalf("sandbox = %+v", sb)
	}
}

func TestRunRejectsEmptyTask(t *testing.T) {
	sb, err := New("openai", "agentkit/macos-base:latest")
	if err != nil {
		t.Fatal(err)
	}
	err = sb.Run(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "task required") {
		t.Fatalf("Run error = %v", err)
	}
}
