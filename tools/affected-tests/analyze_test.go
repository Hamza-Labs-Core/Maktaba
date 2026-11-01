package main

import (
	"strings"
	"testing"
)

// The Mac UI tests went stale in PR #469: the sign-in became a segmented choice, the
// iPhone tests were updated and the Mac ones, which still tap the "With a code" button,
// were not. These fixtures are that shape, cut down.
const (
	connectStrings = `enum Key { case byPassword, byCode }
let en: [Key: String] = [
    .byPassword: "With a password",
    .byCode: "With a code",
]
let ar: [Key: String] = [
    .byCode: "برمز",
]
`
	startViewsOld = `struct SignIn: View {
    var body: some View {
        Button(words.text(.byCode)) { method = .code }
            .accessibilityIdentifier("signin.byCode")
    }
}
`
	startViewsNew = `struct SignIn: View {
    var body: some View {
        Picker(words.text(.signInTitle), selection: $method) {
            Text(words.text(.byCode)).tag(Method.code)
        }
        .pickerStyle(.segmented)
        .accessibilityIdentifier("signin.method")
    }
}
`
	iphoneTest = `final class ConnectUITests: XCTestCase {
    func testByCode() { app.buttons["With a code"].tap() }
}
`
	macTest = `final class PlayerMacMenuUITests: XCTestCase {
    func testSignIn() { app.buttons["With a code"].tap() }
}
`
	kitPath    = "apps/apple/MaktabaKit/Sources/MaktabaKit/StartViews.swift"
	stringsKit = "apps/apple/MaktabaKit/Sources/MaktabaKit/ConnectStrings.swift"
	iphonePath = "apps/apple/UITests/ConnectUITests.swift"
	macPath    = "apps/apple/PlayerMacTests/UI/PlayerMacMenuUITests.swift"
)

func lines(start int, s string) []Line {
	var out []Line
	for i, l := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		out = append(out, Line{N: start + i, Text: l})
	}
	return out
}

// segmentedChange is #469's StartViews edit: the button lines out, the picker lines in.
func segmentedChange() Change {
	return Change{Path: kitPath, Status: 'M',
		Removed: lines(3, "        Button(words.text(.byCode)) { method = .code }\n            .accessibilityIdentifier(\"signin.byCode\")"),
		Added:   lines(3, "        Picker(words.text(.signInTitle), selection: $method) {\n            Text(words.text(.byCode)).tag(Method.code)\n        }\n        .pickerStyle(.segmented)\n        .accessibilityIdentifier(\"signin.method\")")}
}

func appleRepo(iphone, mac string) memRepo {
	return memRepo{
		kitPath:    startViewsNew,
		stringsKit: connectStrings,
		iphonePath: iphone,
		macPath:    mac,
		"apps/apple/MaktabaKit/Tests/MaktabaKitTests/ConnectStringsTests.swift": "func testWords() {}",
	}
}

func TestStaleMacUITestsOfPR469AreCaught(t *testing.T) {
	// the iPhone tests changed, the Mac tests did not
	iphoneNew := strings.ReplaceAll(iphoneTest, `app.buttons["With a code"]`, `app.segmentedControls.buttons["With a code"]`)
	a := analyze(Inputs{
		Changes: []Change{segmentedChange(), {Path: iphonePath, Status: 'M', Added: lines(2, "x")}},
		Repo:    appleRepo(iphoneNew, macTest),
	})
	if len(a.Sources) != 1 {
		t.Fatalf("sources = %v", a.Sources)
	}
	s := a.Sources[0]
	if !contains(s.Tests, macPath) || !contains(s.Tests, iphonePath) {
		t.Fatalf("tests of StartViews = %v, want both UI tests", s.Tests)
	}
	if h := a.Hits[macPath]; h == nil || !h.Via[ViaUI] || !contains(h.Terms, `"With a code"`) {
		t.Fatalf("Mac test hit = %+v, want the UI search on \"With a code\"", h)
	}
	if !contains(s.MissingPlatforms, PAppleMacUI) || contains(s.MissingPlatforms, PAppleIOSUI) {
		t.Fatalf("missing platforms = %v, want only %s", s.MissingPlatforms, PAppleMacUI)
	}
	if !a.Blocked() {
		t.Fatal("the gate passed with the Mac UI tests untouched")
	}

	// both changed: passes
	a = analyze(Inputs{
		Changes: []Change{segmentedChange(), {Path: iphonePath, Status: 'M'}, {Path: macPath, Status: 'M'}},
		Repo:    appleRepo(iphoneNew, strings.ReplaceAll(macTest, "app.buttons", "app.radioGroups.radioButtons")),
	})
	if a.Blocked() {
		var b strings.Builder
		printGate(&b, a)
		t.Fatalf("blocked with both UI test folders updated:\n%s", b.String())
	}
}

func TestTrailerLetsThePushThrough(t *testing.T) {
	a := analyze(Inputs{
		Changes:  []Change{segmentedChange()},
		Repo:     appleRepo(iphoneTest, macTest),
		Messages: []string{"feat: picker\n\nNo-Test-Change: the labels did not change, only the control\n\nCo-Authored-By: x"},
	})
	if a.Blocked() {
		t.Fatal("blocked despite the trailer")
	}
	if a.Override != "the labels did not change, only the control" {
		t.Fatalf("override = %q", a.Override)
	}
	var b strings.Builder
	printGate(&b, a)
	if !strings.Contains(b.String(), "PASS by trailer") {
		t.Fatalf("report does not say the trailer passed it:\n%s", b.String())
	}
}

func TestOverrideReason(t *testing.T) {
	cases := []struct {
		msgs []string
		want string
	}{
		{[]string{"fix\n\nno-test-change: docs only in code comments"}, "docs only in code comments"},
		{[]string{"fix\n\nNo-Test-Change:   spaced reason  \n"}, "spaced reason"},
		{[]string{"fix\n\nNo-Test-Change:"}, ""},          // a reason is required
		{[]string{"fix\n\nNo-Test-Change:   \nnext"}, ""}, // not the next line's text
		{[]string{"mentions No-Test-Change: inline"}, ""}, // only at a line start
		{[]string{"a", "b\n\nNo-Test-Change: second commit"}, "second commit"},
	}
	for _, c := range cases {
		if got := overrideReason(c.msgs); got != c.want {
			t.Errorf("overrideReason(%q) = %q, want %q", c.msgs, got, c.want)
		}
	}
}

func TestStaleReferenceBlocks(t *testing.T) {
	// "With a code" leaves every source; a test still taps it
	kitNew := strings.ReplaceAll(connectStrings, `    .byCode: "With a code",`+"\n", "")
	repo := appleRepo(iphoneTest, macTest)
	repo[stringsKit] = kitNew
	a := analyze(Inputs{
		Changes: []Change{
			{Path: stringsKit, Status: 'M', Removed: lines(4, `    .byCode: "With a code",`)},
			{Path: iphonePath, Status: 'M'}, {Path: macPath, Status: 'M'},
		},
		Repo: repo,
	})
	if len(a.Stale) != 2 {
		t.Fatalf("stale = %+v, want both tests", a.Stale)
	}
	if a.Stale[0].Term != `"With a code"` || a.Stale[0].From != stringsKit {
		t.Fatalf("stale[0] = %+v", a.Stale[0])
	}
	if !a.Blocked() {
		t.Fatal("not blocked with stale tests")
	}

	// moved, not removed: not stale
	repo[startViewsPathOther] = `Text("With a code")`
	a = analyze(Inputs{
		Changes: []Change{{Path: stringsKit, Status: 'M', Removed: lines(4, `    .byCode: "With a code",`)}},
		Repo:    repo,
	})
	if len(a.Stale) != 0 {
		t.Fatalf("stale = %+v, want none (the string is still in a source)", a.Stale)
	}
}

const startViewsPathOther = "apps/apple/Player/Connect/ByCode.swift"

func TestGateDecisions(t *testing.T) {
	repo := memRepo{
		"api/go.mod":                      "module example.com/api\n",
		"api/internal/a/a.go":             "package a",
		"api/internal/a/a_test.go":        "package a",
		"api/internal/b/b.go":             "package b",
		"api/cmd/main.go":                 "package main",
		"api/internal/z/z_test.go":        "package z",
		"streaming/go.mod":                "module example.com/streaming\n",
		"streaming/x/x_test.go":           "package x",
		"apps/desktop/src-tauri/src/x.rs": "fn f() {}\n\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn t() {}\n}\n",
	}
	run := func(chs ...Change) *Analysis { return analyze(Inputs{Changes: chs, Repo: repo}) }

	if a := run(Change{Path: "api/internal/a/a.go", Status: 'M'}, Change{Path: "api/internal/a/a_test.go", Status: 'M'}); a.Blocked() {
		t.Error("source with its test changed: blocked")
	}
	if a := run(Change{Path: "api/internal/a/a.go", Status: 'M'}); !a.Blocked() {
		t.Error("source without its test changed: passed")
	}
	// a deleted source needs nothing
	if a := run(Change{Path: "api/internal/a/a.go", Status: 'D'}); a.Blocked() {
		t.Error("deleted source: blocked")
	}
	// a deleted test does not count as a test change
	if a := run(Change{Path: "api/internal/a/a.go", Status: 'M'}, Change{Path: "api/internal/a/a_test.go", Status: 'D'}); !a.Blocked() {
		t.Error("deleted test counted as updating the tests")
	}
	// no test found: a test of the same Go module will do, another module's will not
	a := run(Change{Path: "api/cmd/main.go", Status: 'M'}, Change{Path: "api/internal/z/z_test.go", Status: 'M'})
	if a.Blocked() || !a.Sources[0].Uncovered {
		t.Errorf("uncovered source with a test of its module changed: blocked=%v uncovered=%v", a.Blocked(), a.Sources[0].Uncovered)
	}
	if a := run(Change{Path: "api/cmd/main.go", Status: 'M'}, Change{Path: "streaming/x/x_test.go", Status: 'M'}); !a.Blocked() {
		t.Error("uncovered source passed on another module's test")
	}
	// docs and config are not gated
	if a := run(Change{Path: "docs/x.md", Status: 'M'}, Change{Path: ".github/workflows/ci.yml", Status: 'M'}); a.Blocked() || len(a.Sources) != 0 {
		t.Error("docs/config gated")
	}
	// Rust: a change in the #[cfg(test)] module counts
	if a := run(Change{Path: "apps/desktop/src-tauri/src/x.rs", Status: 'M', Added: []Line{{N: 6, Text: "    fn t2() {}"}}}); a.Blocked() {
		t.Error("rust inline test change: blocked")
	}
	if a := run(Change{Path: "apps/desktop/src-tauri/src/x.rs", Status: 'M', Added: []Line{{N: 1, Text: "fn f() { 1 }"}}}); !a.Blocked() {
		t.Error("rust change outside its tests: passed")
	}
}

func TestMapSource(t *testing.T) {
	repo := memRepo{
		"api/go.mod":                            "module example.com/api\n",
		"api/internal/store/store.go":           "package store",
		"api/internal/handlers/h/h_test.go":     "package h",
		"api/internal/handlers/h/sub/s_test.go": "package sub",
		"pipeline/src/maktaba_pipeline/x.py":    "",
		"pipeline/tests/test_other.py":          "",
	}
	m := newMap()
	b := newSectionBuilder(granGo)
	b.add("api/internal/store/store.go", "go:api/internal/handlers/h")
	m.Sections["go"] = b.done()
	p := newSectionBuilder(granPython)
	p.add("pipeline/src/maktaba_pipeline/x.py", "python:pipeline/tests/test_other.py::test_uses_x")
	m.Sections["python"] = p.done()

	a := analyze(Inputs{Repo: repo, Map: m, Changes: []Change{
		{Path: "api/internal/store/store.go", Status: 'M'},
		{Path: "pipeline/src/maktaba_pipeline/x.py", Status: 'M'},
		{Path: "api/internal/handlers/h/h_test.go", Status: 'M'},
		{Path: "pipeline/tests/test_other.py", Status: 'M'},
	}})
	h := a.Hits["api/internal/handlers/h/h_test.go"]
	if h == nil || !h.Via[ViaMap] {
		t.Fatalf("the map's Go test was not found: %+v", a.Hits)
	}
	if a.Hits["api/internal/handlers/h/sub/s_test.go"] != nil {
		t.Error("a Go package test ID took a sub-package's tests")
	}
	ph := a.Hits["pipeline/tests/test_other.py"]
	if ph == nil || !ph.Selectors["test_uses_x"] {
		t.Fatalf("the map's pytest node was not kept: %+v", ph)
	}
	if a.Blocked() {
		t.Error("blocked though the map's tests changed")
	}
}

func TestWebImportGraph(t *testing.T) {
	repo := memRepo{
		"web/src/lib/api.ts":              "export const x = 1",
		"web/src/lib/useThing.ts":         `import { x } from "./api";`,
		"web/src/pages/Page.tsx":          "import { useThing } from '@/lib/useThing'\nexport default function P() { return <div>Hi there</div> }",
		"web/src/pages/Page.test.tsx":     `import Page from "./Page";`,
		"web/src/other/Other.test.tsx":    `vi.mock("../lib/api.js")`,
		"web/src/unrelated/U.test.tsx":    `import x from "react"`,
		"web/design-system/Button.tsx":    "export function Button() {}",
		"web/src/components/Uses.tsx":     `export { Button } from "@ds/Button";`,
		"web/src/components/Uses.test.ts": `import "./Uses"`,
	}
	a := analyze(Inputs{Repo: repo, Changes: []Change{{Path: "web/src/lib/api.ts", Status: 'M'}}})
	for _, want := range []string{"web/src/pages/Page.test.tsx", "web/src/other/Other.test.tsx"} {
		if h := a.Hits[want]; h == nil || !h.Via[ViaImports] {
			t.Errorf("%s not found through the import graph", want)
		}
	}
	if a.Hits["web/src/unrelated/U.test.tsx"] != nil {
		t.Error("an unrelated test was taken")
	}
	a = analyze(Inputs{Repo: repo, Changes: []Change{{Path: "web/design-system/Button.tsx", Status: 'M'}}})
	if a.Hits["web/src/components/Uses.test.ts"] == nil {
		t.Error("@ds alias not followed")
	}
}

func TestPlatformsAreGroupedForListing(t *testing.T) {
	a := analyze(Inputs{
		Changes: []Change{segmentedChange()},
		Repo:    appleRepo(iphoneTest, macTest),
	})
	var b strings.Builder
	printList(&b, a, "x...y")
	out := b.String()
	for _, want := range []string{platformTitle[PAppleIOSUI], platformTitle[PAppleMacUI], macPath, `ui: "With a code"`} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, platformTitle[PAppleIOSUI]) > strings.Index(out, platformTitle[PAppleMacUI]) {
		t.Error("platforms out of order")
	}
}
