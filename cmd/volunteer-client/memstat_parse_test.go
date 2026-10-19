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

func TestParseDarwinMemoryStatusLevel_ReturnsFraction(t *testing.T) {
	got, err := parseDarwinMemoryStatusLevel("27\n")
	if err != nil {
		t.Fatalf("parseDarwinMemoryStatusLevel: %v", err)
	}
	if got != 0.27 {
		t.Errorf("fraction = %v, want 0.27", got)
	}
}

func TestParseDarwinMemoryStatusLevel_Malformed_ReturnsError(t *testing.T) {
	for _, in := range []string{"", "not-a-number", "-1", "101"} {
		if _, err := parseDarwinMemoryStatusLevel(in); err == nil {
			t.Errorf("parseDarwinMemoryStatusLevel(%q): expected an error, got nil", in)
		}
	}
}
