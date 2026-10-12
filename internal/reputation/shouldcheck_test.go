package reputation

import (
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/config"
)

func testCfg() config.Config {
	return config.Config{
		FullCheckThreshold: 20,
		CheckDivisorStage1: 10,
		CheckDivisorStage2: 30,
	}
}

func TestShouldCheck_BelowFullCheckThreshold_AlwaysChecks(t *testing.T) {
	cfg := testCfg()
	for totalEvaluated := 0; totalEvaluated < cfg.FullCheckThreshold; totalEvaluated++ {
		if !ShouldCheck(totalEvaluated, totalEvaluated, cfg) {
			t.Errorf("ShouldCheck(totalEvaluated=%d) = false, want true (still under full-check threshold)", totalEvaluated)
		}
	}
}

func TestShouldCheck_Stage1_ChecksOnlyEveryDivisorStage1(t *testing.T) {
	cfg := testCfg()
	consecutivePasses := cfg.FullCheckThreshold // 20: just past the always-check phase

	if ShouldCheck(25, consecutivePasses, cfg) {
		t.Error("ShouldCheck(25) = true, want false (25 is not a multiple of CheckDivisorStage1=10)")
	}
	if !ShouldCheck(30, consecutivePasses, cfg) {
		t.Error("ShouldCheck(30) = false, want true (30 is a multiple of CheckDivisorStage1=10)")
	}
}

func TestShouldCheck_Stage2_ChecksOnlyEveryDivisorStage2(t *testing.T) {
	cfg := testCfg()
	consecutivePasses := 2 * cfg.FullCheckThreshold // 40: sustained long enough for the lowest check rate

	if ShouldCheck(45, consecutivePasses, cfg) {
		t.Error("ShouldCheck(45) = true, want false (45 is not a multiple of CheckDivisorStage2=30)")
	}
	if !ShouldCheck(60, consecutivePasses, cfg) {
		t.Error("ShouldCheck(60) = false, want true (60 is a multiple of CheckDivisorStage2=30)")
	}
}

func TestShouldCheck_NeverReachesZeroRate(t *testing.T) {
	cfg := testCfg()
	// Even a very long clean streak must still be checked sometimes.
	consecutivePasses := 100 * cfg.FullCheckThreshold
	found := false
	for totalEvaluated := 0; totalEvaluated < cfg.CheckDivisorStage2; totalEvaluated++ {
		if ShouldCheck(totalEvaluated, consecutivePasses, cfg) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one check within a full CheckDivisorStage2 window, got none (rate collapsed to zero)")
	}
}

// TestShouldCheck_FailureRestoresFullChecking guards design.md Decision 5:
// "Any single failure resets consecutive_passes to 0 (back to 100%
// checking)". A key's lifetime totalEvaluated never resets, so the
// always-check branch must key off consecutivePasses, not totalEvaluated
// alone — otherwise a volunteer caught cheating stops being scrutinized on
// their very next submission, exactly when scrutiny should be highest.
func TestShouldCheck_FailureRestoresFullChecking(t *testing.T) {
	cfg := testCfg()
	// A long-lived key (way past the lifetime full-check phase) that just
	// failed a check: consecutivePasses resets to 0. totalEvaluated=51 is
	// not a multiple of CheckDivisorStage1(10), so the old divisor-only
	// logic would skip it.
	if !ShouldCheck(51, 0, cfg) {
		t.Error("ShouldCheck after a reset streak = false, want true (a fresh failure must restore 100% checking)")
	}
}
