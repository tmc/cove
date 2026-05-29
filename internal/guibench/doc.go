// Package guibench scores computer-use agents against verifiable native
// macOS tasks (design 047).
//
// A [Task] is a declarative record: a natural-language instruction, the
// cove image to fork from, ordered setup steps, and an [Evaluator] that
// reads live end-state off the guest and scores it. Tasks are parameterized
// templates: a [Schema] of typed parameters is materialized deterministically
// from a seed by [Task.Params], so the gold answer is never static and the
// corpus resists memorization (design 047 §10).
//
// The verifier splits into getters and metrics, mirroring OSWorld. A getter
// pulls an artifact off the live VM through a [Probe]; a [Metric] is a pure
// function Score(result, expected, options) -> float64 in [0,1] that is
// OS-agnostic and unit-testable without a VM. Metrics live in the registry
// returned by [Metrics]; every getter runs over any [Probe], so [FakeProbe]
// exercises them in tests.
//
// Getters are classified by the TCC grant they need (see [Tier], design 047
// §5). Tier-A getters (exec, file, defaults, screen_ocr) need no grant. Tier-B
// getters (sqlite, protected_file, tccdb) need Full Disk Access. Tier-C getters
// (applescript, accessibility) need Apple Events plus Accessibility. The
// Tier-B/C grants are baked into the base image, never done per run, and
// verified by the cove doctor TCC probe before the image is saved; [MaxTier]
// reports the grant level a corpus requires. Every getter that reads persisted
// state flushes first — the sqlite getter checkpoints the write-ahead log, the
// defaults getter reads through cfprefsd — because a read before the OS flushes
// is the single most likely source of false negatives (design 047 §7); [Flush]
// runs a named pre-read flush for getters that read state a different way.
//
// Loading: [Load] reads a directory of per-task JSON files; [Decode] reads
// one task from an io.Reader. Both validate (unknown metric, missing
// conjunction) before returning.
//
// Leaderboard mechanics (design 047 §6, §8, §11). A [Manifest] pins a corpus
// version, a verifier version, and a public/held-out task partition, so a
// result names exactly which corpus and verifier produced it. During scoring the
// runner denies guest egress by default ([TaskEgress] yields a deny-all
// [EgressLockdown]); a task that genuinely needs the network names an allowlist,
// and gold references stay host-side in the verifier, never reachable from the
// guest (the contamination defense). An external party files a [Submission]; a
// result is [TierVerified] only when a maintainer executed the run and the
// submission's versions match the manifest ([StampVerified], [VerifyBundle]) —
// a self-reported number stays [TierUnverified]. Public leaderboard publication
// is gated on a separate privacy/brand decision and is not part of this package.
package guibench
