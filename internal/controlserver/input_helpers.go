// Helpers shared by the input bridge and (via re-exports in package
// main) other callers that need the same coordinate-mapping or
// character-to-keycode logic.
package controlserver

import (
	"github.com/tmc/apple/appkit"
	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

// VMViewAsNSView casts a VZVirtualMachineView to appkit.NSView.
// VZVirtualMachineView inherits from NSView in Objective-C, but the
// generated Go bindings embed objectivec.Object instead of
// appkit.NSView.
func VMViewAsNSView(v vz.VZVirtualMachineView) appkit.NSView {
	return appkit.NSViewFromID(v.ID)
}

// KeyboardEventUnicodeString returns the Unicode string the bridge
// should attach to a keyboard CGEvent for the given command. An
// explicit cmd.Character takes precedence; otherwise a small set of
// special-keycodes (return, tab, delete, escape, space) map to their
// canonical control characters.
func KeyboardEventUnicodeString(cmd *controlpb.KeyCommand) string {
	if cmd == nil {
		return ""
	}
	if cmd.Character != "" {
		return cmd.Character
	}
	switch cmd.KeyCode {
	case 36:
		return "\r"
	case 48:
		return "\t"
	case 51:
		return "\x7f"
	case 53:
		return "\x1b"
	case 49:
		return " "
	default:
		return ""
	}
}

// NeedsWindowCapturePointMapping reports whether the bridge should map
// pointer coordinates from window-capture space to view space for the
// given backend and capture/view dimensions.
func NeedsWindowCapturePointMapping(mode BackendMode, captureW, captureH int, boundsW, contentH float64) bool {
	if captureW <= 0 || captureH <= 0 {
		return false
	}
	if mode == BackendWindow {
		return true
	}
	return float64(captureW) != boundsW || float64(captureH) != contentH
}

// CharKeyCodeInfo holds keycode and shift state for a character.
type CharKeyCodeInfo struct {
	KeyCode uint16
	Shift   bool
}

// CharToKeyCode maps ASCII characters to macOS virtual keycodes.
var CharToKeyCode = map[rune]CharKeyCodeInfo{
	'a': {0, false}, 'b': {11, false}, 'c': {8, false}, 'd': {2, false},
	'e': {14, false}, 'f': {3, false}, 'g': {5, false}, 'h': {4, false},
	'i': {34, false}, 'j': {38, false}, 'k': {40, false}, 'l': {37, false},
	'm': {46, false}, 'n': {45, false}, 'o': {31, false}, 'p': {35, false},
	'q': {12, false}, 'r': {15, false}, 's': {1, false}, 't': {17, false},
	'u': {32, false}, 'v': {9, false}, 'w': {13, false}, 'x': {7, false},
	'y': {16, false}, 'z': {6, false},
	'A': {0, true}, 'B': {11, true}, 'C': {8, true}, 'D': {2, true},
	'E': {14, true}, 'F': {3, true}, 'G': {5, true}, 'H': {4, true},
	'I': {34, true}, 'J': {38, true}, 'K': {40, true}, 'L': {37, true},
	'M': {46, true}, 'N': {45, true}, 'O': {31, true}, 'P': {35, true},
	'Q': {12, true}, 'R': {15, true}, 'S': {1, true}, 'T': {17, true},
	'U': {32, true}, 'V': {9, true}, 'W': {13, true}, 'X': {7, true},
	'Y': {16, true}, 'Z': {6, true},
	'0': {29, false}, '1': {18, false}, '2': {19, false}, '3': {20, false},
	'4': {21, false}, '5': {23, false}, '6': {22, false}, '7': {26, false},
	'8': {28, false}, '9': {25, false},
	' ': {49, false}, '-': {27, false}, '=': {24, false}, '[': {33, false},
	']': {30, false}, '\\': {42, false}, ';': {41, false}, '\'': {39, false},
	',': {43, false}, '.': {47, false}, '/': {44, false}, '`': {50, false},
	'!': {18, true}, '@': {19, true}, '#': {20, true}, '$': {21, true},
	'%': {23, true}, '^': {22, true}, '&': {26, true}, '*': {28, true},
	'(': {25, true}, ')': {29, true}, '_': {27, true}, '+': {24, true},
	'{': {33, true}, '}': {30, true}, '|': {42, true}, '~': {50, true},
	':': {41, true}, '"': {39, true}, '<': {43, true}, '>': {47, true},
	'?':  {44, true},
	'\n': {36, false}, '\t': {48, false},
}

// MapWindowCapturePointToViewPoint maps an absolute window-capture
// pixel point to a view-local point in NSView (bottom-left, top-origin
// input) coordinates.
//
// The capture image (captureW×captureH) is in device pixels: it is the
// whole host window — title bar plus VM content — rendered at the host
// backing scale. The VM view Bounds (boundsW×contentH) are in logical
// points and cover only the content area (the VM view is the window's
// content view, so its width equals the window's logical width).
//
// The two coordinate systems therefore differ by the backing scale S
// AND by the title-bar band that exists in the capture but not in the
// view. Recovering S from the width ratio (both axes describe the same
// window width) lets us convert the capture point into logical points,
// strip the title-bar inset, and flip Y into the view's bottom-left
// origin. Doing the inset subtraction in device pixels against a
// point-valued content height (the historical bug) conflated the two
// units and clamped nearly every click to the top of the view — a
// bottom-of-content target such as a consent-dialog button landed on
// the menu bar.
func MapWindowCapturePointToViewPoint(x, y float64, captureW, captureH int, boundsW, contentH float64) (viewX, viewY float64) {
	if captureW <= 0 || captureH <= 0 || boundsW <= 0 || contentH <= 0 {
		return x, contentH - y
	}

	// Backing scale: device pixels per logical point. Width is a pure
	// content dimension in both systems, so their ratio is the scale.
	scale := float64(captureW) / boundsW

	viewX = x / scale

	// Convert the capture Y (device px, measured from the window top)
	// into logical points from the window top, then drop the title-bar
	// band so 0 == top of VM content.
	yTopPt := y / scale
	windowHpt := float64(captureH) / scale
	titleBarPt := windowHpt - contentH
	if titleBarPt < 0 {
		titleBarPt = 0
	}
	contentY := yTopPt - titleBarPt
	if contentY < 0 {
		contentY = 0
	}
	if contentY > contentH {
		contentY = contentH
	}
	viewY = contentH - contentY
	return viewX, viewY
}

// MapNormalizedWindowCapturePointToViewPoint is the normalized-input
// variant of MapWindowCapturePointToViewPoint.
func MapNormalizedWindowCapturePointToViewPoint(x, y float64, captureW, captureH int, boundsW, contentH float64) (viewX, viewY float64) {
	if captureW <= 0 || captureH <= 0 {
		return x * boundsW, (1.0 - y) * contentH
	}
	return MapWindowCapturePointToViewPoint(
		x*float64(captureW),
		y*float64(captureH),
		captureW,
		captureH,
		boundsW,
		contentH,
	)
}
