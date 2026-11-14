//go:build darwin

package hostload

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func loadAvg() ([3]float64, bool) {
	// struct loadavg { fixpt_t ldavg[3]; long fscale; }: three uint32, padding, an int64
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(b) < 24 {
		return [3]float64{}, false
	}
	scale := float64(binary.LittleEndian.Uint64(b[16:24]))
	if scale <= 0 {
		return [3]float64{}, false
	}
	var out [3]float64
	for i := range out {
		out[i] = float64(binary.LittleEndian.Uint32(b[4*i:4*i+4])) / scale
	}
	return out, true
}
