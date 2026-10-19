//go:build darwin

package main

import (
	"fmt"
	"os/exec"
)

func availableMemoryFraction() (float64, error) {
	out, err := exec.Command("sysctl", "-n", "kern.memorystatus_level").Output()
	if err != nil {
		return 0, fmt.Errorf("memstat: run sysctl kern.memorystatus_level: %w", err)
	}
	return parseDarwinMemoryStatusLevel(string(out))
}
