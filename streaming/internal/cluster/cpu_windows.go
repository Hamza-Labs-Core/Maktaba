package cluster

import (
	"syscall"
	"unsafe"
)

var procGetSystemTimes = syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemTimes")

func cpuTimes() (idle, total uint64, ok bool) {
	var i, k, u syscall.Filetime
	r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u)))
	if r == 0 {
		return 0, 0, false
	}
	ft := func(f syscall.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	// kernel time includes idle time
	return ft(i), ft(k) + ft(u), true
}

func loadFallback() float64 { return 0 }
