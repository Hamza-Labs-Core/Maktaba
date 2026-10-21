package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// detail_codes.json is the fixed list of the codes of every detail the app's
// start steps show (/processes' detail_code), each with the names of its
// values (detail_params), in the order the steps give them (Epic 38 stories
// 38.4 and 38.12; contract-update.md has it as a table). The app's screens
// translate each code (web/src/i18n, setup.starting.detail.<code>), and their
// test reads this file; the start steps of story 38.12 (serve_appbundle.go's
// detailCodes) embed it. One list, so the server and the screens cannot drift.
func TestDetailCodesFile(t *testing.T) {
	raw, err := os.ReadFile("detail_codes.json")
	if err != nil {
		t.Fatal(err)
	}
	var codes map[string][]string
	if err := json.Unmarshal(raw, &codes); err != nil {
		t.Fatalf("detail_codes.json: %v", err)
	}
	if len(codes) == 0 {
		t.Fatal("detail_codes.json lists no code")
	}
	snake := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	for code, names := range codes {
		if !snake.MatchString(code) {
			t.Errorf("code %q is not snake_case", code)
		}
		seen := map[string]bool{}
		for _, n := range names {
			if !snake.MatchString(n) {
				t.Errorf("%s: value %q is not snake_case", code, n)
			}
			if seen[n] {
				t.Errorf("%s: value %q twice", code, n)
			}
			seen[n] = true
		}
	}
}
