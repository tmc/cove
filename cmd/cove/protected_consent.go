// protected_consent.go - honest detection of macOS protected consent sheets.
//
// Some macOS system dialogs (NetworkExtension proxy/VPN configuration consent,
// TCC permission prompts) can only be approved by a real human HID click. No
// public host-side API delivers a synthetic click that the window server trusts
// for these sheets: the AppKit view path moves the cursor but does not activate
// them, the private sendPointerNSEvent path is still a synthetic event into the
// absolute pointing device, in-guest CGEvents are TCC-rejected, and a virtual
// IOHIDUserDevice needs an Apple-restricted entitlement cove cannot self-sign.
//
// Rather than let an OCR click-text automation silently no-op against such a
// sheet (looking like success), cove detects the context and fails honestly
// with ErrRequiresHumanClick before dispatching the synthetic click.

package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrRequiresHumanClick is returned by OCR click automation when the target is
// an approval control on a macOS protected consent sheet, which no synthetic
// click can activate. Callers should surface it as a request for a real human
// click rather than retry.
var ErrRequiresHumanClick = errors.New("target requires a real human click: macOS protected consent sheet cannot be activated by a synthetic click")

// consentSheetPhrases are substrings (lowercased) that uniquely identify a
// protected macOS consent sheet in the on-screen OCR text. Each phrase is
// specific enough that it does not appear on ordinary application UI.
var consentSheetPhrases = []string{
	"would like to add proxy configurations",
	"would like to add vpn configurations",
	"would like to filter network content",
	"added vpn configurations",  // grammar seen on some macOS builds
	"add proxy configurations",  // shorter form when OCR clips the prefix
	"add vpn configurations",    // shorter form
	"is trying to add a new network configuration",
}

// consentApprovalLabels are button labels (lowercased) that approve a consent
// sheet. Only clicks aimed at one of these are gated; a click aimed at "Don't
// Allow"/"Cancel"/"Deny" or any ordinary control is left alone.
var consentApprovalLabels = []string{
	"allow",
	"add",
}

// detectProtectedConsent reports whether screenText (the full OCR text of the
// current screen) shows a protected macOS consent sheet, and if so returns a
// short human-readable label for it. It is a pure function of the OCR text so
// it can be unit-tested without a VM.
func detectProtectedConsent(screenText string) (label string, ok bool) {
	lower := strings.ToLower(screenText)
	for _, phrase := range consentSheetPhrases {
		if strings.Contains(lower, phrase) {
			return phrase, true
		}
	}
	return "", false
}

// isConsentApprovalTarget reports whether the click target text is an approval
// control (Allow/Add) as opposed to a decline control or ordinary UI. The match
// is exact (case-insensitive, trimmed) so that targeting "Don't Allow",
// "Allow Once for a different app", or a label that merely contains "add" is
// NOT treated as an approval click.
func isConsentApprovalTarget(target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	return slices.Contains(consentApprovalLabels, t)
}

// consentGateBlocksClick reports whether an OCR click-text at target should be
// refused because it would attempt to approve a protected consent sheet. Both
// conditions must hold: the screen shows a consent sheet AND the target is an
// approval control. This keeps ordinary OCR clicks (and even decline clicks on
// a consent sheet) at their current behavior.
func consentGateBlocksClick(screenText, target string) (label string, blocked bool) {
	if !isConsentApprovalTarget(target) {
		return "", false
	}
	label, ok := detectProtectedConsent(screenText)
	if !ok {
		return "", false
	}
	return label, true
}

// wrapConsentErr builds the error returned when a click is refused for target
// on a protected consent sheet labelled label. It wraps ErrRequiresHumanClick
// so callers can branch with errors.Is.
func wrapConsentErr(target, label string) error {
	return fmt.Errorf("click %q on protected consent sheet %q: %w", target, label, ErrRequiresHumanClick)
}
