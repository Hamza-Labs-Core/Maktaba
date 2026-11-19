//go:build linux

package hostinfo

import (
	"testing"
	"time"
)

func TestStatCPU(t *testing.T) {
	// a command name with spaces and parentheses; utime 250, stime 50 ticks
	line := "42 (a (b) c) S 1 42 42 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 1 0 100 1000 10"
	if d, ok := statCPU(line); !ok || d != 3*time.Second {
		t.Fatalf("%v %v", d, ok)
	}
	// four years of one core's time: past where ticks*time.Second overflowed
	big := "1 (x) S 1 1 1 0 -1 0 0 0 0 0 12623040000 0 0 0 20 0 1 0 1 1 1"
	if d, ok := statCPU(big); !ok || d != 12623040000*10*time.Millisecond {
		t.Fatalf("a long-lived process: %v %v", d, ok)
	}
	if _, ok := statCPU("42 (x) S 1"); ok {
		t.Fatal("a short line was read")
	}
}
