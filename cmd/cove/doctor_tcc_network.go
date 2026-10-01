package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
	agentstate "github.com/tmc/cove/internal/agent"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

const networkVolumeApprovalFile = "network-volume-approval.json"

type networkVolumeApproval struct {
	VZAgent bool `json:"vzAgent"`
}

var networkVolumePromptPattern = regexp.MustCompile(`(^|[[:space:]"])vz-agent"? would like to access files on a network volume`)

func networkVolumePrompt(text string) bool {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	text = strings.NewReplacer("“", "\"", "”", "\"").Replace(text)
	return networkVolumePromptPattern.MatchString(text)
}

func networkVolumeApprovalEnabled(vmDirectory string) bool {
	data, err := os.ReadFile(filepath.Join(vmDirectory, networkVolumeApprovalFile))
	if err != nil {
		return false
	}
	var preference networkVolumeApproval
	return json.Unmarshal(data, &preference) == nil && preference.VZAgent
}

func runTCCNetworkVolumes(args []string) error {
	fs := flag.NewFlagSet("doctor tcc-network-volumes", flag.ContinueOnError)
	name := fs.String("vm", "", "VM name")
	enable := fs.Bool("enable", false, "Approve future vz-agent network-volume prompts for this VM")
	disable := fs.Bool("disable", false, "Stop approving future network-volume prompts")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove doctor tcc-network-volumes [-vm name] [-enable|-disable]\n\nRecord consent for Cove to click Allow on the specific vz-agent network-volume\nprompt. The VM GUI must be open. This does not grant Full Disk Access.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *enable && *disable {
		return fmt.Errorf("doctor tcc-network-volumes: choose -enable or -disable")
	}
	target := currentVMSelection()
	selected := *name
	if selected == "" {
		selected = vmName
	}
	if selected != "" {
		var err error
		target, err = requireExistingVMSelection("doctor tcc-network-volumes", selected)
		if err != nil {
			return err
		}
	}
	directory := target.Directory
	if *enable || *disable {
		data, _ := json.Marshal(networkVolumeApproval{VZAgent: *enable})
		if err := os.WriteFile(filepath.Join(directory, networkVolumeApprovalFile), append(data, '\n'), 0600); err != nil {
			return fmt.Errorf("save network-volume consent: %w", err)
		}
	}
	fmt.Printf("vz-agent network-volume prompt autoapproval: %t\n", networkVolumeApprovalEnabled(directory))
	fmt.Println("Applies while the VM GUI is open on a Cove runtime with this feature; macOS retains the actual grant. Other permission prompts require separate approval.")
	return nil
}

func (s *ControlServer) monitorNetworkVolumeApproval() {
	if agentstate.Platform(s.vmDir) != agentstate.PlatformMacOS {
		return
	}
	ctx := s.lifecycleContext()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	pending, failedLogged := false, false
	pendingTicks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !networkVolumeApprovalEnabled(s.vmDir) || s.bridge.HealthSnapshot().UserStatus != "connected" {
			continue
		}
		s.mu.Lock()
		gui := s.gui
		s.mu.Unlock()
		if gui == nil {
			continue
		}
		guiStatus := gui.Status()
		if !guiStatus.Headed || !guiStatus.WindowReady {
			continue
		}
		img, captureError := s.captureDisplayImage()
		if captureError != "" {
			continue
		}
		observations, err := s.getOCR().RecognizeText(img)
		if err != nil {
			continue
		}
		matches := networkVolumeObservations(observations)
		allText := make([]string, len(matches))
		for i, match := range matches {
			allText[i] = match.Text
		}
		present := networkVolumePrompt(strings.Join(allText, " "))
		if pending {
			if !present {
				slog.Info("network-volume approval: prompt dismissed")
				pending, failedLogged, pendingTicks = false, false, 0
			} else {
				pendingTicks++
				if pendingTicks >= 3 && !failedLogged {
					slog.Warn("network-volume approval: prompt remains; approve it manually inside the guest")
					failedLogged = true
				}
			}
			continue
		}
		x, y, found := networkVolumeAllowTarget(matches)
		if !found {
			continue
		}
		if _, blocked := consentGateBlocksClick(strings.Join(allText, " "), "Allow"); blocked {
			continue
		}
		response := s.sendMouseEvent(&controlpb.MouseCommand{X: x, Y: y, Action: "click"})
		if !response.Success {
			slog.Warn("network-volume approval: click failed; approve it manually inside the guest")
			pending, failedLogged = true, true
			continue
		}
		pending = true
	}
}

func networkVolumeObservations(observations []ocrx.TextObservation) []*controlpb.OCRMatch {
	matches := make([]*controlpb.OCRMatch, 0, len(observations))
	for _, observation := range observations {
		box := observation.BoundingBox
		matches = append(matches, &controlpb.OCRMatch{Text: observation.Text, X: box.Origin.X, Y: 1 - box.Origin.Y - box.Size.Height, Width: box.Size.Width, Height: box.Size.Height})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Y < matches[j].Y })
	return matches
}

func networkVolumeAllowTarget(matches []*controlpb.OCRMatch) (float64, float64, bool) {
	var targets []*controlpb.OCRMatch
	for _, allow := range matches {
		if allow.Text != "Allow" {
			continue
		}
		for _, decline := range matches {
			if decline.Text != "Don't Allow" && decline.Text != "Don’t Allow" {
				continue
			}
			if math.Abs(allow.Y-decline.Y) > 0.025 || decline.X >= allow.X || allow.X-decline.X > 0.25 {
				continue
			}
			var body []*controlpb.OCRMatch
			for _, item := range matches {
				if item.Y >= allow.Y-0.22 && item.Y < allow.Y-0.015 && item.X >= decline.X-0.12 && item.X+item.Width <= allow.X+allow.Width+0.12 {
					body = append(body, item)
				}
			}
			sort.Slice(body, func(i, j int) bool { return body[i].Y < body[j].Y })
			text := make([]string, len(body))
			for i, item := range body {
				text[i] = item.Text
			}
			if networkVolumePrompt(strings.Join(text, " ")) {
				targets = append(targets, allow)
			}
		}
	}
	if len(targets) != 1 {
		return 0, 0, false
	}
	target := targets[0]
	x, y := target.X+target.Width/2, target.Y+target.Height/2
	if x <= 0 || x >= 1 || y <= 0 || y >= 1 {
		return 0, 0, false
	}
	return x, y, true
}
