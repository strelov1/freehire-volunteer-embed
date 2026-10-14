package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
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

var vmStatFieldPattern = regexp.MustCompile(`^(Pages \w+):\s+(\d+)\.`)

// parseDarwinVMStat computes the available-memory fraction from `vm_stat`'s
// output. "Available" has no single authoritative definition on macOS;
// free + inactive + speculative pages is a common, deliberately
// conservative approximation of memory that can be reclaimed or reused
// without swapping -- good enough for a safety signal, not meant to match
// Activity Monitor exactly.
func parseDarwinVMStat(output string, pageSize, totalMemBytes uint64) (float64, error) {
	if totalMemBytes == 0 {
		return 0, fmt.Errorf("memstat: totalMemBytes must not be zero")
	}

	fields := map[string]uint64{}
	for _, line := range strings.Split(output, "\n") {
		m := vmStatFieldPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			continue
		}
		fields[m[1]] = v
	}

	free, haveFree := fields["Pages free"]
	inactive, haveInactive := fields["Pages inactive"]
	speculative, haveSpeculative := fields["Pages speculative"]
	if !haveFree || !haveInactive || !haveSpeculative {
		return 0, fmt.Errorf("memstat: vm_stat output missing one of free/inactive/speculative pages")
	}

	availableBytes := (free + inactive + speculative) * pageSize
	return float64(availableBytes) / float64(totalMemBytes), nil
}
