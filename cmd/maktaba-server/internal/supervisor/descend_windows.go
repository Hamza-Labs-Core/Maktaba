//go:build windows

package supervisor

// procEntry is one process of a snapshot: its parent's pid, and when it was created (the
// OS's start time, layout.ProcessStart; 0 when it could not be read).
type procEntry struct {
	pid, ppid int
	created   uint64
}

// descendantsOf are the processes of procs below roots, by their parents. A parent pid is
// only a name: the parent may have ended and its pid gone to another process since. So a
// process counts as a child only when it was created at or after the process now holding
// its parent's pid (one that predates it is another's child, its real parent gone), and
// when both times are known.
func descendantsOf(roots []int, procs []procEntry) map[int]bool {
	byPID := make(map[int]procEntry, len(procs))
	for _, p := range procs {
		byPID[p.pid] = p
	}
	in := make(map[int]bool, len(roots))
	for _, r := range roots {
		in[r] = true
	}
	out := map[int]bool{}
	for _, p := range procs {
		seen := map[int]bool{p.pid: true}
		for c := p; ; {
			parent, ok := byPID[c.ppid]
			if !ok || seen[parent.pid] || c.created == 0 || parent.created == 0 || c.created < parent.created {
				break
			}
			if in[parent.pid] {
				out[p.pid] = true
				break
			}
			seen[parent.pid] = true
			c = parent
		}
	}
	return out
}
