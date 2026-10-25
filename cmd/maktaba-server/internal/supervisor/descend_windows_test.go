//go:build windows

package supervisor

import "testing"

// Review (re-check L): a process whose parent pid is now another process's (it predates
// the process holding that pid) is no descendant: never ended, nor kept, with an adopted
// TV process. Its real descendants are, down the line.
func TestDescendantsCheckCreationTimes(t *testing.T) {
	procs := []procEntry{
		{pid: 100, ppid: 4, created: 10},   // the adopted slot
		{pid: 200, ppid: 100, created: 20}, // its ffmpeg
		{pid: 400, ppid: 200, created: 30}, // ffmpeg's own child
		{pid: 300, ppid: 100, created: 5},  // older than 100: its parent pid was another process's
		{pid: 500, ppid: 300, created: 40}, // below that one
		{pid: 600, ppid: 100, created: 0},  // its time could not be read
		{pid: 700, ppid: 800, created: 50}, // a loop of reused pids
		{pid: 800, ppid: 700, created: 60},
	}
	got := descendantsOf([]int{100}, procs)
	for pid, want := range map[int]bool{200: true, 400: true, 300: false, 500: false, 600: false, 700: false, 800: false, 100: false} {
		if got[pid] != want {
			t.Errorf("pid %d: descendant %v, want %v", pid, got[pid], want)
		}
	}
}
