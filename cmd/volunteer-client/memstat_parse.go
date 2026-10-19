package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// parseLinuxMemInfo computes the available-memory fraction from the
// contents of /proc/meminfo. MemAvailable is the kernel's own estimate of
// memory available for a new process without swapping -- a better signal
// than MemFree alone, which excludes reclaimable caches.
func parseLinuxMemInfo(r io.Reader) (float64, error) {
	var availableKB, totalKB uint64
	var haveAvailable, haveTotal bool

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemAvailable:"):
			availableKB, haveAvailable = parseMemInfoValue(line)
		case strings.HasPrefix(line, "MemTotal:"):
			totalKB, haveTotal = parseMemInfoValue(line)
		}
	}
	if !haveAvailable {
		return 0, fmt.Errorf("memstat: MemAvailable not found in /proc/meminfo")
	}
	if !haveTotal || totalKB == 0 {
		return 0, fmt.Errorf("memstat: MemTotal not found (or zero) in /proc/meminfo")
	}
	return float64(availableKB) / float64(totalKB), nil
}

func parseMemInfoValue(line string) (uint64, bool) {
	fields := strings.Fields(line) // e.g. "MemAvailable:", "5000000", "kB"
	if len(fields) < 2 {
		return 0, false
	}
	v, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseDarwinMemoryStatusLevel reads `sysctl -n kern.memorystatus_level`:
// the kernel's own percentage of memory available before it starts
// applying pressure, the same figure `memory_pressure` prints as
// "System-wide memory free percentage". It counts the file cache macOS
// will give back on demand. Summing vm_stat's free, inactive and
// speculative pages instead missed that cache: on a 32 GB Mac with 27%
// free it read 14%, below lowMemoryHoldFraction, so the chunk size could
// never grow past wherever it last shrank to — one text per call.
func parseDarwinMemoryStatusLevel(output string) (float64, error) {
	level, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, fmt.Errorf("memstat: parse kern.memorystatus_level: %w", err)
	}
	if level < 0 || level > 100 {
		return 0, fmt.Errorf("memstat: kern.memorystatus_level = %d, want 0..100", level)
	}
	return float64(level) / 100, nil
}
