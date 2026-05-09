package testtier

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// AssertNoTmpLeaks is the EC3 sweep: integration tests own a
// t.TempDir per test, but services we drive (FFmpeg, goose, etc.) can
// drop /tmp/maktaba-* working dirs that t.TempDir doesn't track. Call
// this from TestMain after m.Run() and let it surface anything
// remaining as a non-zero exit.
//
// pattern is a glob like "/tmp/maktaba-*". exit is the exit code from
// m.Run(); the function returns either exit (when nothing leaked) or
// 1 (when something did and exit was 0), preserving any pre-existing
// failure code.
func AssertNoTmpLeaks(out io.Writer, pattern string, exit int) int {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Fprintf(out, "tmp-leak sweep: glob %q failed: %v\n", pattern, err)
		if exit == 0 {
			return 1
		}
		return exit
	}
	if len(matches) == 0 {
		return exit
	}
	fmt.Fprintf(out, "tmp-leak sweep: %d entries left under %q (Story 20.1 EC3):\n",
		len(matches), pattern)
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil {
			fmt.Fprintf(out, "  %s (stat err: %v)\n", m, statErr)
			continue
		}
		fmt.Fprintf(out, "  %s (mode=%s, size=%d)\n", m, info.Mode(), info.Size())
	}
	if exit == 0 {
		return 1
	}
	return exit
}
