package agentsandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Sandbox struct {
	Provider string
	Image    string
	CoveBin  string
}

func New(provider, image string) (*Sandbox, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil, errors.New("agentsandbox: provider required")
	}
	if strings.TrimSpace(image) == "" {
		return nil, errors.New("agentsandbox: image required")
	}
	return &Sandbox{Provider: provider, Image: image, CoveBin: "cove"}, nil
}

func (s *Sandbox) Run(ctx context.Context, task string) error {
	if s == nil {
		return errors.New("agentsandbox: nil sandbox")
	}
	if strings.TrimSpace(task) == "" {
		return errors.New("agentsandbox: task required")
	}
	bin := s.CoveBin
	if bin == "" {
		bin = "cove"
	}
	cmd := exec.CommandContext(ctx, bin, "agent-sandbox", "run",
		"--provider", s.Provider,
		"--image", s.Image,
		"--task", task,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("agentsandbox: run %s: %w", s.Provider, err)
	}
	return nil
}

func (s *Sandbox) Close() error { return nil }
