package main

import (
	"strings"
	"testing"
)

func TestParseLinuxMemInfo_RealFormat_ReturnsAvailableFraction(t *testing.T) {
	// Real /proc/meminfo always has more fields than this; parsing must
	// only need the two it actually uses.
	fixture := `MemTotal:       16384000 kB
MemFree:         1234000 kB
MemAvailable:    5000000 kB
Buffers:          200000 kB
Cached:          3000000 kB
SwapCached:            0 kB
`
	got, err := parseLinuxMemInfo(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("parseLinuxMemInfo: %v", err)
	}
	want := 5000000.0 / 16384000.0
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("fraction = %v, want %v", got, want)
	}
}

func TestParseLinuxMemInfo_MissingMemAvailable_ReturnsError(t *testing.T) {
	fixture := `MemTotal:       16384000 kB
MemFree:         1234000 kB
`
	_, err := parseLinuxMemInfo(strings.NewReader(fixture))
	if err == nil {
		t.Fatal("expected an error when MemAvailable is missing, got nil")
	}
}

func TestParseLinuxMemInfo_MalformedValue_ReturnsError(t *testing.T) {
	fixture := `MemTotal:       16384000 kB
MemAvailable:    not-a-number kB
`
	_, err := parseLinuxMemInfo(strings.NewReader(fixture))
	if err == nil {
		t.Fatal("expected an error for a malformed MemAvailable value, got nil")
	}
}

func TestParseDarwinVMStat_RealFormat_ReturnsAvailableFraction(t *testing.T) {
	// Captured from a real `vm_stat` run during this project's own
	// development.
	fixture := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                     4594.
Pages active:                                 311675.
Pages inactive:                               302381.
Pages speculative:                              8854.
Pages throttled:                                   0.
Pages wired down:                             232345.
`
	const pageSize = 16384
	const totalMemBytes = 16 * 1024 * 1024 * 1024 // 16 GiB

	got, err := parseDarwinVMStat(fixture, pageSize, totalMemBytes)
	if err != nil {
		t.Fatalf("parseDarwinVMStat: %v", err)
	}

	availablePages := uint64(4594 + 302381 + 8854) // free + inactive + speculative
	want := float64(availablePages*pageSize) / float64(totalMemBytes)
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("fraction = %v, want %v", got, want)
	}
}

func TestParseDarwinVMStat_MissingField_ReturnsError(t *testing.T) {
	fixture := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages active:                                 311675.
`
	_, err := parseDarwinVMStat(fixture, 16384, 16*1024*1024*1024)
	if err == nil {
		t.Fatal("expected an error when Pages free is missing, got nil")
	}
}

func TestParseDarwinVMStat_ZeroTotalMemBytes_ReturnsError(t *testing.T) {
	fixture := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                     4594.
Pages inactive:                               302381.
Pages speculative:                              8854.
`
	_, err := parseDarwinVMStat(fixture, 16384, 0)
	if err == nil {
		t.Fatal("expected an error for zero total memory, got nil")
	}
}
