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
// &stepError{code: "code", …} and setupEvent{Code: "code", …}; and the move's
// (internal/adopt: coded("code", …), codedf("code", …)), which the adopt step
// fails with as they are.
//
// One pattern per form, each starting with a literal, so the scan jumps from
// one literal to the next (strings.Index) over the ~1.4MB of the package's
// source. One alternation of them all has no literal prefix: the matcher then
// walks every byte through the whole alternation, which took over a second
// under -race and broke the per-test time budget (tools/test-budget).
var stepCode = []*regexp.Regexp{
	regexp.MustCompile(`failStep\("([a-z_]+)"`),
	regexp.MustCompile(`stepError\{code:\s*"([a-z_]+)"`),
	regexp.MustCompile(`setupEvent\{[^}]*Code:\s*"([a-z_]+)"`),
	regexp.MustCompile(`codedf?\("([a-z_]+)"`),
}

// stepCodesIn returns the step codes in one file's source.
func stepCodesIn(src string) map[string]bool {
	codes := map[string]bool{}
	for _, re := range stepCode {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			// coded( and codedf( only as whole words: not encoded(…).
			if re == stepCode[3] && m[0] > 0 && isWordByte(src[m[0]-1]) {
				continue
			}
			codes[src[m[2]:m[3]]] = true
		}
	}
	return codes
}

func isWordByte(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// Every code a setup step fails with has its words in both of the web's
// languages (setup.error.<code>): the screens never fall back to the step's
// English (contract-setup, "codes").
func TestEveryStepErrorHasItsWords(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	moves, err := filepath.Glob(filepath.Join("internal", "adopt", "*.go"))
	if err != nil || len(moves) == 0 {
		t.Fatalf("the move's files: %v", err)
	}
	files = append(files, moves...)
	codes := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for code := range stepCodesIn(string(src)) {
			codes[code] = true
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

// stepCodesIn finds each form of a step code, and coded( only as a whole word.
func TestStepCodesInFindsEveryForm(t *testing.T) {
	src := `failStep("one", err)
	&stepError{code: "two", msg: "x"}
	setupEvent{Step: "s", Code: "three"}
	coded("four", "x")
	codedf("five", "%s", x)
	encoded("not_this", x)
	failStep("Not_lower", x)`
	got := stepCodesIn(src)
	want := []string{"one", "two", "three", "four", "five"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, c := range want {
		if !got[c] {
			t.Errorf("missed %q in %v", c, got)
		}
	}
}

// moveNote finds the codes of the move's plan notes: note("code", …).
var moveNote = regexp.MustCompile(`\bnote\("([a-z_]+)"`)

// Every note of the move's plan (internal/adopt) has its words in both of the
// web's languages: setup.move.note.<code> (a warning or a problem),
// setup.move.carry.<code> or setup.move.nocarry.<code> (a setting carried or
// not), as the screens show it.
func TestEveryMoveNoteHasItsWords(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("internal", "adopt", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("the move's files: %v", err)
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
		for _, m := range moveNote.FindAllStringSubmatch(string(src), -1) {
			codes[m[1]] = true
		}
	}
	if len(codes) < 10 {
		t.Fatalf("found %d notes: the pattern no longer finds them", len(codes))
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
			if strings.TrimSpace(words["setup.move.note."+code]+words["setup.move.carry."+code]+words["setup.move.nocarry."+code]) == "" {
				t.Errorf("%s.json has no setup.move.note, carry or nocarry .%s", lang, code)
			}
		}
	}
}
