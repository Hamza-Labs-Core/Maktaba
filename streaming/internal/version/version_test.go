package version

import (
	"strings"
	"testing"
)

func TestStringIncludesAllFields(t *testing.T) {
	t.Parallel()

	Version = "v1.2.3"
	Commit = "abc1234"
	BuildDate = "1700000000"
	t.Cleanup(func() {
		Version = "unknown"
		Commit = "unknown"
		BuildDate = "unknown"
	})

	got := String()
	for _, want := range []string{"v1.2.3", "abc1234", "1700000000"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want to contain %q", got, want)
		}
	}
}
