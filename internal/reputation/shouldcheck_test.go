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
	for totalChecked := 0; totalChecked < cfg.FullCheckThreshold; totalChecked++ {
		if !ShouldCheck(totalChecked, totalChecked, cfg) {
			t.Errorf("ShouldCheck(totalChecked=%d) = false, want true (still under full-check threshold)", totalChecked)
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
	for totalChecked := 0; totalChecked < cfg.CheckDivisorStage2; totalChecked++ {
		if ShouldCheck(totalChecked, consecutivePasses, cfg) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one check within a full CheckDivisorStage2 window, got none (rate collapsed to zero)")
	}
}
