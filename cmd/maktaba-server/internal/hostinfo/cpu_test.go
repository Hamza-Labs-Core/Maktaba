package hostinfo

import (
	"os"
	"testing"
	"time"
)

// This test's own processor time is read by its pid, grows while it works, and a pid that
// is gone has none (story 54.52).
func TestProcessCPU(t *testing.T) {
	before := ProcessCPU([]int{os.Getpid(), 1 << 30})
	if _, has := before[1<<30]; has {
		t.Fatal("a pid that does not exist has processor time")
	}
	b, ok := before[os.Getpid()]
	if !ok {
		t.Fatal("this test's processor time was not read")
	}
	// burn about 300 ms of processor
	end := time.Now().Add(300 * time.Millisecond)
	x := 0
	for time.Now().Before(end) {
		x++
	}
	_ = x
	after := ProcessCPU([]int{os.Getpid()})[os.Getpid()]
	if after-b < 100*time.Millisecond {
		t.Fatalf("processor time %v then %v after 300 ms of work", b, after)
	}
}

func TestParsePSTime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:00.03":     30 * time.Millisecond,
		"1:02.50":     62500 * time.Millisecond,
		"01:02:03":    time.Hour + 2*time.Minute + 3*time.Second,
		"2-01:00:00":  49 * time.Hour,
		"10:00:00.00": 10 * time.Hour,
	} {
		if got, ok := parsePSTime(in); !ok || got != want {
			t.Errorf("%q: %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "12", "a:b", "1:61", "x-1:00"} {
		if _, ok := parsePSTime(bad); ok {
			t.Errorf("%q read", bad)
		}
	}
}
