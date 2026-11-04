//go:build windows

package hostinfo

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// FreeMemory is the physical memory available, in bytes (0 when unknown).
func FreeMemory() uint64 {
	st := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return 0
	}
	return st.availPhys
}

// processMemoryCounters is PROCESS_MEMORY_COUNTERS.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// ProcessRSS is the working set of each running pid, in bytes. A pid that is gone (or
// cannot be opened) has no entry.
func ProcessRSS(pids []int) map[int]uint64 {
	out := map[int]uint64{}
	for _, p := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p))
		if err != nil {
			continue
		}
		c := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
		if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); r != 0 {
			out[p] = uint64(c.workingSetSize)
		}
		_ = windows.CloseHandle(h)
	}
	return out
}
