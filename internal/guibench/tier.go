package guibench

import "sort"

// Tier classifies a getter (and a backend) by the TCC privilege grant it needs
// on a fresh fork (design 047 §5). A naive "non-Apple-Events getters need no
// grant" assumption is wrong on modern macOS: reading a protected app's SQLite
// store or many ~/Library paths needs Full Disk Access, which a fresh VM lacks,
// and the read otherwise silently fails. So getters are classified by tier and
// the base image is provisioned with exactly the grants its corpus needs,
// verified by the `cove doctor` TCC probe before the image is saved.
//
// The underlying values are the labels "A" < "B" < "C", so an ordinary string
// comparison orders the tiers by privilege. The [Backend] interface reports its
// grant level as a Tier.
type Tier string

const (
	// TierA needs no grant: exec/stdout, user-space file, defaults (cfprefsd),
	// and screen OCR.
	TierA Tier = "A"
	// TierB needs Full Disk Access: protected app SQLite stores, TCC-protected
	// ~/Library paths, and the TCC database itself.
	TierB Tier = "B"
	// TierC needs Apple Events + Accessibility automation: AppleScript/JXA and
	// the Accessibility (AX) tree. These are TCC services independent of FDA.
	TierC Tier = "C"
)

// Grant returns the human-readable grant the tier requires, for a doctor /
// image-check report.
func (t Tier) Grant() string {
	switch t {
	case TierA:
		return "none"
	case TierB:
		return "Full Disk Access"
	case TierC:
		return "Apple Events + Accessibility"
	default:
		return "unknown"
	}
}

// MaxTier returns the highest privilege tier any getter in the tasks requires.
// It is the grant level the base image must carry for the corpus to run without
// the silent verifier failures a missing grant causes (design 047 §5, §12). An
// empty corpus yields TierA. Tier values sort A < B < C (their string order),
// so the comparison is a plain string max.
func MaxTier(tasks []*Task) Tier {
	maxTier := TierA
	for _, t := range tasks {
		if g := t.Evaluator.Result.Tier(); g > maxTier {
			maxTier = g
		}
		if t.Evaluator.Expected != nil {
			if g := t.Evaluator.Expected.Tier(); g > maxTier {
				maxTier = g
			}
		}
	}
	return maxTier
}

// getterKindNames returns the registered getter kinds in sorted order, used by
// the verifier-version surface hash (see version.go).
func getterKindNames() []string {
	out := make([]string, 0, len(getterKinds))
	for k := range getterKinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
