package reputation

import "github.com/strelov1/freehire-volunteer-embed/internal/config"

// ShouldCheck decides whether a submission from a key with the given
// history should be spot-checked against the trusted TEI, per design.md
// Decision 5: every submission is checked until totalChecked reaches
// FullCheckThreshold; after that, check rate is a step function of how
// long the key's current clean streak (consecutivePasses) has run, using
// totalChecked as a deterministic counter so the rate never collapses to
// zero.
func ShouldCheck(totalChecked, consecutivePasses int, cfg config.Config) bool {
	if totalChecked < cfg.FullCheckThreshold {
		return true
	}
	divisor := cfg.CheckDivisorStage1
	if consecutivePasses >= 2*cfg.FullCheckThreshold {
		divisor = cfg.CheckDivisorStage2
	}
	return totalChecked%divisor == 0
}
