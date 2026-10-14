//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct. golang.org/x/sys/windows
// does not wrap GlobalMemoryStatusEx itself, so this calls kernel32.dll
// directly -- the same pattern that package uses internally for APIs it
// hasn't wrapped.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

// availableMemoryFraction reports the fraction of system memory currently
// available, straight from the OS -- independent of the local embedder's
// own responsiveness. See memstat_parse.go for why this matters: a sudden
// OOM kill gives no slow-call warning a latency-based signal could react
// to in time.
func availableMemoryFraction() (float64, error) {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))

	ret, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 {
		return 0, fmt.Errorf("memstat: GlobalMemoryStatusEx: %w", err)
	}
	if status.TotalPhys == 0 {
		return 0, fmt.Errorf("memstat: GlobalMemoryStatusEx reported zero total physical memory")
	}
	return float64(status.AvailPhys) / float64(status.TotalPhys), nil
}
