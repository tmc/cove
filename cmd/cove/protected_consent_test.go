package main

import (
	"errors"
	"testing"
)

// A representative OCR dump of the NetworkExtension proxy-configuration consent
// sheet, as it appears on screen (button labels included).
const proxyConsentScreen = `"Proxyman" Would Like to Add Proxy Configurations
All network activity on this Mac may be filtered or monitored when using a proxy.
Don't Allow    Allow`

const vpnConsentScreen = `"MyVPN" Would Like to Add VPN Configurations
All network activity on this Mac may be filtered or monitored when using VPN.
Don't Allow    Allow`

// An ordinary application screen with buttons that happen to include approval
// words, to guard against false positives.
const ordinaryScreen = `Untitled Document
Add Row    Allow Comments    Save    Cancel`

func TestDetectProtectedConsent(t *testing.T) {
	tests := []struct {
		name       string
		screen     string
		wantOK     bool
		wantLabel  string // substring the returned label must contain (when ok)
	}{
		{"proxy consent", proxyConsentScreen, true, "proxy configurations"},
		{"vpn consent", vpnConsentScreen, true, "vpn configurations"},
		{"content filter", `"App" Would Like to Filter Network Content`, true, "filter network content"},
		{"ordinary screen", ordinaryScreen, false, ""},
		{"empty", "", false, ""},
		{"finder", "Finder    File    Edit    View    Go", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, ok := detectProtectedConsent(tt.screen)
			if ok != tt.wantOK {
				t.Fatalf("detectProtectedConsent ok=%v, want %v (label=%q)", ok, tt.wantOK, label)
			}
			if ok && !contains(label, tt.wantLabel) {
				t.Fatalf("label=%q, want substring %q", label, tt.wantLabel)
			}
		})
	}
}

func TestIsConsentApprovalTarget(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{"Allow", true},
		{"allow", true},
		{"  Allow  ", true},
		{"Add", true},
		{"Don't Allow", false},
		{"Allow Once", false}, // exact match only; not a bare approval
		{"Cancel", false},
		{"Deny", false},
		{"Add Row", false}, // contains "add" but is not a bare approval
		{"Continue", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isConsentApprovalTarget(tt.target); got != tt.want {
			t.Errorf("isConsentApprovalTarget(%q)=%v, want %v", tt.target, got, tt.want)
		}
	}
}

// TestConsentGateBlocksClick is the core regression: the gate must block ONLY
// when an approval control is clicked on a real consent sheet, and must leave
// every other combination (ordinary UI, decline clicks, ordinary buttons on a
// consent sheet) at pass-through behavior.
func TestConsentGateBlocksClick(t *testing.T) {
	tests := []struct {
		name        string
		screen      string
		target      string
		wantBlocked bool
	}{
		{"allow on proxy consent -> blocked", proxyConsentScreen, "Allow", true},
		{"add on vpn consent -> blocked", vpnConsentScreen, "Add", true},
		{"decline on consent -> pass", proxyConsentScreen, "Don't Allow", false},
		{"ordinary allow-comments button -> pass", ordinaryScreen, "Allow Comments", false},
		{"ordinary add-row on ordinary screen -> pass", ordinaryScreen, "Add Row", false},
		{"bare allow but no consent sheet -> pass", ordinaryScreen, "Allow", false},
		{"bare add but no consent sheet -> pass", "Notes App\nAdd    Delete", "Add", false},
		{"finder continue -> pass", "Finder\nContinue", "Continue", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, blocked := consentGateBlocksClick(tt.screen, tt.target)
			if blocked != tt.wantBlocked {
				t.Errorf("consentGateBlocksClick(screen=%q, target=%q) blocked=%v, want %v",
					tt.name, tt.target, blocked, tt.wantBlocked)
			}
		})
	}
}

// TestErrRequiresHumanClick_Wraps confirms the sentinel is detectable via
// errors.Is when wrapped by the click path, so socket/CLI callers can branch on
// it rather than string-match.
func TestErrRequiresHumanClick_Wraps(t *testing.T) {
	wrapped := errWrap()
	if !errors.Is(wrapped, ErrRequiresHumanClick) {
		t.Fatalf("errors.Is(wrapped, ErrRequiresHumanClick) = false; want true")
	}
}

// errWrap mirrors how OCRClickTextWithOptions wraps the sentinel.
func errWrap() error {
	label, blocked := consentGateBlocksClick(proxyConsentScreen, "Allow")
	if !blocked {
		return nil
	}
	return wrapConsentErr("Allow", label)
}

func contains(s, sub string) bool {
	return sub == "" || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
