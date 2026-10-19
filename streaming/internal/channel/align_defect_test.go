//go:build !tvdefect

package channel

import "testing"

// Without the tvdefect tag a BuildDefect is refused, never applied.
func TestBuildDefectRefusedWithoutTheTag(t *testing.T) {
	checkBuildDefect("") // no defect: fine
	defer func() {
		if recover() == nil {
			t.Fatal("a BuildDefect was accepted in a build without the tvdefect tag")
		}
		if TSPMTPID != 0x1000 {
			t.Fatalf("the PIDs moved: PMT %#x", TSPMTPID)
		}
	}()
	checkBuildDefect("pids")
}
