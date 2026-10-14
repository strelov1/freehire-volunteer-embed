//go:build !linux && !darwin && !windows

package main

import "fmt"

// availableMemoryFraction has no implementation on this platform. Callers
// treat the error as "unknown" and fall back to latency-only signals,
// rather than failing to run at all.
func availableMemoryFraction() (float64, error) {
	return 0, fmt.Errorf("memstat: not supported on this platform")
}
