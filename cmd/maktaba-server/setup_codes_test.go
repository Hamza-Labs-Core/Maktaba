package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stepCode finds the codes a setup step fails with: failStep("code", …),
// &stepError{code: "code", …} and setupEvent{Code: "code", …}.
var stepCode = regexp.MustCompile(`failStep\("([a-z_]+)"|stepError\{code:\s*"([a-z_]+)"|setupEvent\{[^}]*Code:\s*"([a-z_]+)"`)

// Every code a setup step fails with has its words in both of the web's
// languages (setup.error.<code>): the screens never fall back to the step's
// English (contract-setup, "codes").
func TestEveryStepErrorHasItsWords(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range stepCode.FindAllStringSubmatch(string(src), -1) {
			codes[m[1]+m[2]+m[3]] = true
		}
	}
	if len(codes) < 20 {
		t.Fatalf("found %d codes: the pattern no longer finds them", len(codes))
	}
	for _, lang := range []string{"en", "ar"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "i18n", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var words map[string]string
		if err := json.Unmarshal(raw, &words); err != nil {
			t.Fatal(err)
		}
		for code := range codes {
			if strings.TrimSpace(words["setup.error."+code]) == "" {
				t.Errorf("%s.json has no setup.error.%s", lang, code)
			}
		}
	}
}
