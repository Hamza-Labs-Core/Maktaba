package billing

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Epic 58 story 58.04: the store reviewers' home relays at the lowest plan's cap. The lowest
// plan with the relay is Basic (Free has none), and the review home's deploy names it.
func TestLowestRelayPlanIsTheReviewHomes(t *testing.T) {
	if got := LowestRelayPlan(); got != PlanBasic {
		t.Fatalf("LowestRelayPlan() = %q, want %q", got, PlanBasic)
	}
	if Tiers[LowestRelayPlan()].RelayCapKbps == 0 {
		t.Fatal("the lowest relay plan has no cap: the review home would relay at full quality")
	}
	// the deploy's file (cloud/internal/billing -> the repository root)
	f, err := os.Open(filepath.Join("..", "..", "..", "deploy", "review-home", "review-home.env.example"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	plan := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "REVIEW_HOME_PLAN="); ok {
			plan = v
		}
	}
	if plan != LowestRelayPlan() {
		t.Fatalf("deploy/review-home/review-home.env.example: REVIEW_HOME_PLAN=%q, want %q (the lowest plan with the relay)", plan, LowestRelayPlan())
	}
}
