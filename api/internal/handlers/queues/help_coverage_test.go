package queues

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Epic 41: the web app links every failure cause to /help/failures#<cause>
// and every waiting reason to /help/processing#waiting-<reason> ("_" → "-").
// A cause or reason added here without its help entry in both languages
// fails this test.

var helpAnchor = regexp.MustCompile(`(?m)^#{1,6}\s.*\{#([a-z0-9-]+)\}\s*$`)

func helpAnchors(t *testing.T, locale, topic string) map[string]bool {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "web", "src", "help", "content", locale, topic+".md")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skipf("web/ is not in this checkout (%s)", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range helpAnchor.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	return out
}

func helpCode(code string) string { return strings.ReplaceAll(strings.ToLower(code), "_", "-") }

func TestEveryCauseHasHelp(t *testing.T) {
	for _, locale := range []string{"en", "ar"} {
		have := helpAnchors(t, locale, "failures")
		for cause := range causeLabels {
			if !have[helpCode(cause)] {
				t.Errorf("%s/failures.md: no entry {#%s} for cause %q", locale, helpCode(cause), cause)
			}
		}
		if !have["other"] {
			t.Errorf("%s/failures.md: no entry {#other} for generated causes", locale)
		}
	}
}

func TestEveryWaitingReasonHasHelp(t *testing.T) {
	reasons := []WaitingReason{
		WaitDisabled, WaitNotConfigured, WaitPausedAll, WaitPaused, WaitAwaitingReview,
		WaitNoWorker, WaitPoolFull, WaitDebouncing, WaitBackoff, WaitServiceRetrying,
		WaitUpstream, WaitIdle,
	}
	for _, locale := range []string{"en", "ar"} {
		have := helpAnchors(t, locale, "processing")
		for _, r := range reasons {
			a := "waiting-" + helpCode(string(r))
			if !have[a] {
				t.Errorf("%s/processing.md: no entry {#%s} for waiting reason %q", locale, a, r)
			}
		}
	}
}
