package main

import "testing"

// Epic 58 story 58.04: `maktaba-server review-home` hands every argument to maktaba-api's
// review-home command (seed.sh runs it in the review home's container).
func TestReviewHomeDelegates(t *testing.T) {
	c, _, err := newRootCmd().Find([]string{"review-home"})
	if err != nil || c.Name() != "review-home" {
		t.Fatalf("review-home: %v, %v", c, err)
	}
	if !c.DisableFlagParsing {
		t.Error("review-home parses flags itself: maktaba-api's --manifest and --dsn would be refused")
	}
}
