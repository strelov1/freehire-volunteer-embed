package main

import "testing"

// TestAvailableMemoryFraction_RunsOnThisMachine_ReturnsSaneFraction exercises
// the real, platform-specific implementation (not a fixture) -- the one
// thing a pure fixture test can't prove is that the actual OS call this
// machine makes still works and returns something sane.
func TestAvailableMemoryFraction_RunsOnThisMachine_ReturnsSaneFraction(t *testing.T) {
	frac, err := availableMemoryFraction()
	if err != nil {
		t.Fatalf("availableMemoryFraction: %v", err)
	}
	if frac < 0 || frac > 1 {
		t.Errorf("fraction = %v, want a value in [0, 1]", frac)
	}
	t.Logf("available memory fraction on this machine: %.3f", frac)
}
