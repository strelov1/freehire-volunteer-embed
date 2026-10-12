package ratelimit

import "testing"

func TestAllow_FirstRequestsWithinBurst_AreAllowed(t *testing.T) {
	l := New(1, 3) // 1 req/s refill, burst of 3

	for i := 0; i < 3; i++ {
		if !l.Allow(1) {
			t.Errorf("request %d within burst was denied, want allowed", i)
		}
	}
}

func TestAllow_RequestBeyondBurst_IsDenied(t *testing.T) {
	l := New(1, 3)
	for i := 0; i < 3; i++ {
		l.Allow(1)
	}

	if l.Allow(1) {
		t.Error("4th immediate request beyond burst was allowed, want denied")
	}
}

func TestAllow_DifferentKeys_HaveIndependentBudgets(t *testing.T) {
	l := New(1, 1)

	if !l.Allow(1) {
		t.Fatal("key 1's first request was denied, want allowed")
	}
	if !l.Allow(2) {
		t.Error("key 2's first request was denied by key 1's budget, want allowed (independent limiters)")
	}
}
