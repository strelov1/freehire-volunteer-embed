//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

var vmStatPageSizePattern = regexp.MustCompile(`page size of (\d+) bytes`)

// availableMemoryFraction reports the fraction of system memory currently
// available, straight from the OS -- independent of the local embedder's
// own responsiveness. See memstat_parse.go for why this matters: a sudden
// OOM kill gives no slow-call warning a latency-based signal could react
// to in time. macOS has no /proc equivalent, so this shells out to the
// same two commands Activity Monitor's data ultimately comes from.
func availableMemoryFraction() (float64, error) {
	vmStatOut, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("memstat: run vm_stat: %w", err)
	}

	pageSizeMatch := vmStatPageSizePattern.FindStringSubmatch(string(vmStatOut))
	if pageSizeMatch == nil {
		return 0, fmt.Errorf("memstat: could not find page size in vm_stat output")
	}
	pageSize, err := strconv.ParseUint(pageSizeMatch[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("memstat: parse page size: %w", err)
	}

	sysctlOut, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0, fmt.Errorf("memstat: run sysctl hw.memsize: %w", err)
	}
	totalMemBytes, err := strconv.ParseUint(string(bytes.TrimSpace(sysctlOut)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("memstat: parse hw.memsize: %w", err)
	}

	return parseDarwinVMStat(string(vmStatOut), pageSize, totalMemBytes)
}
