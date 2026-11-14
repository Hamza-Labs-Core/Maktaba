package hostload

import (
	"context"
	"os/exec"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// topTimeout bounds the listing: on a machine this busy, ps itself can crawl.
const topTimeout = 3 * time.Second

// TopProcs lists the n programs using the most CPU with ps (nil: it cannot, as on
// Windows, in a container without ps, or when ps does not answer in time).
func TopProcs(ctx context.Context, n int) []Proc {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, topTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-Ao", "pid=,pcpu=,comm=").Output()
	if err != nil {
		return nil
	}
	return parseTop(string(out), n)
}

// parseTop reads "pid pcpu command" lines and keeps the n with the most CPU, leaving
// out those that use none. A command is named by its file, without its folder.
func parseTop(out string, n int) []Proc {
	ps := []Proc{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		cpu, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil || cpu <= 0 {
			continue
		}
		name := path.Base(strings.Join(f[2:], " "))
		ps = append(ps, Proc{PID: pid, Name: name, CPUPct: cpu})
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].CPUPct > ps[j].CPUPct })
	if len(ps) > n {
		ps = ps[:n]
	}
	return ps
}
