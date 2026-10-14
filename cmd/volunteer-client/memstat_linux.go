//go:build linux

package main

import (
	"fmt"
	"os"
)

// availableMemoryFraction reports the fraction of system memory currently
// available, straight from the OS -- independent of the local embedder's
// own responsiveness. See memstat_parse.go for why this matters: a sudden
// OOM kill gives no slow-call warning a latency-based signal could react
// to in time.
func availableMemoryFraction() (float64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("memstat: open /proc/meminfo: %w", err)
	}
	defer f.Close()
	return parseLinuxMemInfo(f)
}
