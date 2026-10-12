package reputation

import "github.com/strelov1/freehire-volunteer-embed/internal/config"

// ShouldCheck decides whether a submission from a key with the given
// history should be spot-checked against the trusted TEI, per design.md
// Decision 5: every submission is checked until a key's clean streak
// (consecutivePasses) reaches FullCheckThreshold — including right after a
// failure resets that streak to 0, so a volunteer just caught submitting a
// bad vector is scrutinized hardest on its very next submission, not
// forgiven because its lifetime totalEvaluated had already climbed past
// the threshold once. After that, check rate is a step function of how
// long the streak has run, using totalEvaluated as a deterministic
// counter so the rate never collapses to zero.
//
// Contract for callers: totalEvaluated must be incremented once per call to
// this function — i.e. once per submission routed through verify.Evaluate —
// whether or not that call actually performed a check. Incrementing it only
// when a check is actually performed would let the counter settle on a
// value that is never a multiple of the active divisor, permanently
// stopping checks for that key.
func ShouldCheck(totalEvaluated, consecutivePasses int, cfg config.Config) bool {
	if totalEvaluated < cfg.FullCheckThreshold || consecutivePasses < cfg.FullCheckThreshold {
		return true
	}
	divisor := cfg.CheckDivisorStage1
	if consecutivePasses >= 2*cfg.FullCheckThreshold {
		divisor = cfg.CheckDivisorStage2
	}
	return totalEvaluated%divisor == 0
}
