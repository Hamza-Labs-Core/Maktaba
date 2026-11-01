package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	for in, want := range map[string]Range{
		"origin/main...HEAD": {Base: "origin/main", Head: "HEAD", ThreeDot: true},
		"abc...":             {Base: "abc", Head: "HEAD", ThreeDot: true},
		"a..b":               {Base: "a", Head: "b"},
	} {
		got, err := parseRange(in)
		if err != nil || got != want {
			t.Errorf("parseRange(%q) = %+v, %v; want %+v", in, got, err, want)
		}
		if got.String() != in && !strings.HasSuffix(in, "...") {
			t.Errorf("%q round-trips as %q", in, got.String())
		}
	}
	for _, bad := range []string{"main", "...HEAD", "..x"} {
		if _, err := parseRange(bad); err == nil {
			t.Errorf("parseRange(%q) took it", bad)
		}
	}
	if r, _ := parseRange("a...b"); r.LogRange() != "a..b" {
		t.Errorf("log range = %q", r.LogRange())
	}
}

func TestParseDiff(t *testing.T) {
	diff := `diff --git a/x.go b/x.go
index 1..2 100644
--- a/x.go
+++ b/x.go
@@ -3,2 +3,3 @@ func f() {
-	old()
--- not a header, a removed line starting with dashes
+	new()
+	newer()
+++ an added line starting with pluses
diff --git a/new.swift b/new.swift
new file mode 100644
--- /dev/null
+++ b/new.swift
@@ -0,0 +1 @@
+let a = "Hello there"
diff --git a/gone.kt b/gone.kt
deleted file mode 100644
--- a/gone.kt
+++ /dev/null
@@ -1 +0,0 @@
-val x = 1
diff --git a/old/name.py b/new/name.py
similarity index 90%
rename from old/name.py
rename to new/name.py
--- a/old/name.py
+++ b/new/name.py
@@ -1 +1 @@
-x = 1
+x = 2
diff --git a/img.png b/img.png
Binary files a/img.png and b/img.png differ
`
	chs := parseDiff([]byte(diff))
	if len(chs) != 5 {
		t.Fatalf("got %d changes: %+v", len(chs), chs)
	}
	x := chs[0]
	if x.Path != "x.go" || x.Status != 'M' || len(x.Removed) != 2 || len(x.Added) != 3 {
		t.Fatalf("x.go = %+v", x)
	}
	if x.Added[0] != (Line{N: 3, Text: "\tnew()"}) || x.Added[2].Text != "++ an added line starting with pluses" {
		t.Errorf("x.go added = %+v", x.Added)
	}
	if x.Removed[1].N != 4 {
		t.Errorf("x.go removed numbering = %+v", x.Removed)
	}
	if chs[1].Path != "new.swift" || chs[1].Status != 'A' || chs[1].Added[0].N != 1 {
		t.Errorf("new.swift = %+v", chs[1])
	}
	if chs[2].Path != "gone.kt" || chs[2].Status != 'D' {
		t.Errorf("gone.kt = %+v", chs[2])
	}
	if chs[3].Path != "new/name.py" || chs[3].OldPath != "old/name.py" || chs[3].Status != 'R' {
		t.Errorf("rename = %+v", chs[3])
	}
	if chs[4].Path != "img.png" {
		t.Errorf("binary = %+v", chs[4])
	}
}

func TestMapParseAndMerge(t *testing.T) {
	good := `{"version":1,"commit":"abc","sections":{"go":{"granularity":"package","tests":["go:api/a","go:api/b"],"files":{"api/x.go":[0,1]}}}}`
	m, err := parseMap([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.TestsFor("api/x.go"); !reflect.DeepEqual(got, []string{"go:api/a", "go:api/b"}) {
		t.Fatalf("TestsFor = %v", got)
	}
	if m.TestsFor("api/none.go") != nil {
		t.Error("unknown file has tests")
	}
	var nilMap *CoverageMap
	if nilMap.TestsFor("x") != nil {
		t.Error("nil map has tests")
	}
	for _, bad := range []string{
		`{"version":2,"sections":{}}`,
		`{"version":1,"sections":{"go":{"tests":["a"],"files":{"x":[1]}}}}`,
		`{"version":1,"sections":{"go":{"tests":["a"],"files":{"x":[-1]}}}}`,
		`not json`,
	} {
		if _, err := parseMap([]byte(bad)); err == nil {
			t.Errorf("parseMap(%s) took it", bad)
		}
	}

	next := newMap()
	b := newSectionBuilder(granPython)
	b.add("pipeline/src/p.py", "python:pipeline/tests/test_p.py::test_a")
	b.add("pipeline/src/p.py", "python:pipeline/tests/test_p.py::test_a") // once only
	next.Sections["python"] = b.done()
	next.Commit = "def"
	m.merge(next)
	if m.Sections["go"] == nil || m.Sections["python"] == nil || m.Commit != "def" {
		t.Fatalf("merge lost a section: %+v", m.Sections)
	}
	if got := m.TestsFor("pipeline/src/p.py"); len(got) != 1 {
		t.Errorf("duplicate test IDs: %v", got)
	}

	file := filepath.Join(t.TempDir(), "m.json")
	if err := m.save(file); err != nil {
		t.Fatal(err)
	}
	back, err := loadMap(file)
	if err != nil || !reflect.DeepEqual(back.Sections["python"].Tests, m.Sections["python"].Tests) {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

func TestParseTestID(t *testing.T) {
	id, ok := parseTestID("python:pipeline/tests/a/test_b.py::TestC::test_d")
	if !ok || id.Platform != "python" || id.Path != "pipeline/tests/a/test_b.py" || id.Selector != "TestC::test_d" {
		t.Fatalf("parseTestID = %+v %v", id, ok)
	}
	if id.String() != "python:pipeline/tests/a/test_b.py::TestC::test_d" {
		t.Errorf("String = %q", id.String())
	}
	for _, bad := range []string{"", "nocolon", ":path", "go:"} {
		if _, ok := parseTestID(bad); ok {
			t.Errorf("parseTestID(%q) took it", bad)
		}
	}
}

func TestNaming(t *testing.T) {
	files := []string{
		"api/go.mod",
		"api/internal/x/foo.go", "api/internal/x/foo_test.go", "api/internal/x/foo_pg_test.go", "api/internal/x/bar_test.go",
		"pipeline/src/maktaba_pipeline/search/models.py", "pipeline/tests/search/test_models.py", "pipeline/tests/db/test_models.py",
		"pipeline/src/maktaba_pipeline/audio/probe.py", "pipeline/tests/audio/test_probe.py", "pipeline/tests/test_probe_more.py",
		"web/src/lib/format.ts", "web/src/lib/format.test.ts", "web/src/lib/format.cases.test.ts", "web/src/lib/formatter.test.ts",
		"apps/android/player/src/main/kotlin/a/HomeScreen.kt",
		"apps/android/player/src/test/kotlin/a/HomeScreenTest.kt",
		"apps/android/player/src/test/kotlin/a/HomeScreenshotTest.kt",
		"apps/android/player/src/test/kotlin/a/HomeScreenScreenshotTest.kt",
		"apps/tv/android/app/src/test/java/a/HomeScreenTest.kt",
		"apps/apple/Player/Home/HomeScreen.swift", "apps/apple/UITests/PlayerHomeUITests.swift",
		"apps/apple/UITests/HomeScreenTests.swift", "apps/apple/PlayerMacTests/UI/PlayerMacHomeUITests.swift",
		"apps/apple/UITests/PlayerHomeworkUITests.swift",
		"tools/release/tag-guard.sh", "tools/release/tag-guard-test.sh",
		"tools/apple/check-string-catalogs.py", "tools/apple/test_check_string_catalogs.py",
	}
	idx := newFileIndex(files)
	check := func(src string, wantNamed, wantKin []string) {
		t.Helper()
		named, kin := namingTests(src, idx)
		sort.Strings(named)
		sort.Strings(kin)
		sort.Strings(wantNamed)
		sort.Strings(wantKin)
		if !reflect.DeepEqual(named, wantNamed) || !reflect.DeepEqual(kin, wantKin) {
			t.Errorf("%s: named %v kin %v; want %v / %v", src, named, kin, wantNamed, wantKin)
		}
	}
	check("api/internal/x/foo.go", []string{"api/internal/x/foo_test.go", "api/internal/x/foo_pg_test.go"}, []string{"api/internal/x/bar_test.go"})
	check("pipeline/src/maktaba_pipeline/search/models.py", []string{"pipeline/tests/search/test_models.py"}, nil)
	check("pipeline/src/maktaba_pipeline/audio/probe.py", []string{"pipeline/tests/audio/test_probe.py"}, nil)
	check("web/src/lib/format.ts", []string{"web/src/lib/format.test.ts", "web/src/lib/format.cases.test.ts"}, nil)
	check("apps/android/player/src/main/kotlin/a/HomeScreen.kt", []string{
		"apps/android/player/src/test/kotlin/a/HomeScreenTest.kt",
		"apps/android/player/src/test/kotlin/a/HomeScreenScreenshotTest.kt",
		"apps/tv/android/app/src/test/java/a/HomeScreenTest.kt",
	}, nil)
	check("apps/apple/Player/Home/HomeScreen.swift", []string{"apps/apple/UITests/HomeScreenTests.swift"},
		[]string{"apps/apple/UITests/PlayerHomeUITests.swift", "apps/apple/PlayerMacTests/UI/PlayerMacHomeUITests.swift"})
	check("tools/release/tag-guard.sh", []string{"tools/release/tag-guard-test.sh"}, nil)
	check("tools/apple/check-string-catalogs.py", []string{"tools/apple/test_check_string_catalogs.py"}, nil)
}

func TestClassify(t *testing.T) {
	idx := newFileIndex([]string{"api/go.mod", "tools/affected-tests/go.mod"})
	cases := map[string]struct {
		kind     Kind
		platform string
	}{
		"api/internal/a/a.go":                                      {KindSource, PGo},
		"api/internal/a/a_test.go":                                 {KindTest, PGo},
		"api/internal/a/testdata/x.go":                             {KindTestSupport, PGo},
		"api/gen/x.pb.go":                                          {KindOther, PGo},
		"tools/affected-tests/main.go":                             {KindSource, PGo},
		"pipeline/src/maktaba_pipeline/x.py":                       {KindSource, PPython},
		"pipeline/tests/a/test_x.py":                               {KindTest, PPython},
		"pipeline/tests/conftest.py":                               {KindTestSupport, PPython},
		"web/src/a/B.tsx":                                          {KindSource, PWeb},
		"web/src/a/B.test.tsx":                                     {KindTest, PWeb},
		"web/vite.config.ts":                                       {KindOther, PWeb},
		"apps/android/player/src/main/kotlin/A.kt":                 {KindSource, PAndroid},
		"apps/android/player/src/phone/kotlin/A.kt":                {KindSource, PAndroid},
		"apps/android/player/src/test/kotlin/ATest.kt":             {KindTest, PAndroid},
		"apps/android/core/src/androidTest/k/BTest.kt":             {KindTest, PAndroidInstr},
		"apps/tv/android/app/src/test/java/CTest.kt":               {KindTest, PAndroidTV},
		"apps/desktop-player/src/test/kotlin/DTest.kt":             {KindTest, PDesktopPlayer},
		"apps/apple/MaktabaKit/Sources/MaktabaKit/A.swift":         {KindSource, PSourceApple},
		"apps/apple/MaktabaKit/Tests/MaktabaKitTests/ATests.swift": {KindTest, PAppleKit},
		"apps/apple/UITests/PlayerHomeUITests.swift":               {KindTest, PAppleIOSUI},
		"apps/apple/UITests/MockHome.swift":                        {KindTestSupport, PAppleIOSUI},
		"apps/apple/PlayerMacTests/PlayerMenuTests.swift":          {KindTest, PAppleMac},
		"apps/apple/PlayerMacTests/UI/PlayerMacMenuUITests.swift":  {KindTest, PAppleMacUI},
		"apps/apple/Player/Home/HomeScreen.swift":                  {KindSource, PSourceApple},
		"apps/apple/Player/Shell/Shell.xcstrings":                  {KindOther, PSourceApple},
		"apps/tv/tvos/MaktabaTV/ContentView.swift":                 {KindSource, PSourceTVOS},
		"apps/tv/tvos/MaktabaTVUITests/TVLiveUITests.swift":        {KindTest, PTVOSUI},
		"apps/desktop/src-tauri/src/main.rs":                       {KindSource, PRust},
		"tools/release/tag-guard-test.sh":                          {KindTest, PScripts},
		"docs/ci.md":                                               {KindOther, ""},
	}
	for p, want := range cases {
		c := idx.class(p)
		if c.Kind != want.kind || c.Platform != want.platform {
			t.Errorf("classify(%s) = %+v, want kind %d platform %q", p, c, want.kind, want.platform)
		}
	}
}

func TestStringTerm(t *testing.T) {
	keep := []string{"With a code", "Home", "Sign in", "برمز", "Loading…", "Can't connect"}
	drop := []string{"play.fill", "signin.method", "abc", "OK", "HTTP", "PlayerHome", "/api/me", "https://x.y",
		"x", "%lld", "Hello \\(name)", "${x} items", "com.hamzalabs.maktaba", "home", "AUTO_PLAY", "123 456"}
	for _, s := range keep {
		if _, ok := stringTerm(s); !ok {
			t.Errorf("stringTerm(%q) dropped it", s)
		}
	}
	for _, s := range drop {
		if _, ok := stringTerm(s); ok {
			t.Errorf("stringTerm(%q) kept it", s)
		}
	}
}

func TestMatchTerm(t *testing.T) {
	src := `app.buttons["With a code"].tap(); onNodeWithText(ctx.getString(R.string.sign_in)); id = "player.row.42"`
	cases := []struct {
		term Term
		want bool
	}{
		{Term{Text: "With a code"}, true},
		{Term{Text: "With a"}, false}, // not the whole literal
		{Term{Text: "R.string.sign_in", Mode: MatchWord}, true},
		{Term{Text: "R.string.sign", Mode: MatchWord}, false},
		{Term{Text: "player.row.", Mode: MatchPrefix}, true},
		{Term{Text: "row.", Mode: MatchPrefix}, false},
	}
	for _, c := range cases {
		if got := matchTerm(src, c.term); got != c.want {
			t.Errorf("matchTerm(%+v) = %v", c.term, got)
		}
	}
}

func TestLineTerms(t *testing.T) {
	tb := tables{en: map[string]string{"login.submit": "Sign in"}, swift: map[string][]string{"byCode": {"With a code", "برمز"}}}
	got := func(p, line string) []string {
		var out []string
		for _, t := range lineTerms(p, []Line{{N: 1, Text: line}}, tb) {
			out = append(out, t.label())
		}
		sort.Strings(out)
		return out
	}
	eq := func(name string, have, want []string) {
		t.Helper()
		sort.Strings(want)
		if !reflect.DeepEqual(have, want) {
			t.Errorf("%s: %v, want %v", name, have, want)
		}
	}
	eq("swift key", got("a/B.swift", `Text(words.text(.byCode)).accessibilityIdentifier("signin.code")`), []string{`"With a code"`, `"برمز"`, `"signin.code"`})
	eq("swift literal", got("a/B.swift", `Button("Try again") { }`), []string{`"Try again"`})
	eq("swift comment", got("a/B.swift", `// "Try again"`), nil)
	eq("tsx", got("web/src/A.tsx", `<button data-testid="go-btn">{t("login.submit")}</button> <p>Nothing to see</p>`),
		[]string{`"go-btn"`, `"login.submit"`, `"Sign in"`, `"Nothing to see"`})
	eq("kotlin", got("apps/android/player/src/main/kotlin/A.kt", `Text("Up next", Modifier.testTag("player.upNext"))`), []string{`"Up next"`, `"player.upNext"`})
	eq("strings.xml", got("apps/android/player/src/main/res/values/strings.xml", `<string name="sign_in">Sign in</string>`), []string{"R.string.sign_in", `"Sign in"`})
	eq("xcstrings key", got("apps/apple/Player/Shell/Shell.xcstrings", `    "Live TV" : {`), []string{`"Live TV"`})
	eq("xcstrings nested", got("apps/apple/Player/Shell/Shell.xcstrings", `        "localizations" : {`), nil)
	eq("en.json", got("web/src/i18n/en.json", `  "nav.library": "Library",`), []string{`"nav.library"`, `"Library"`})
	eq("interpolated id", got("a/B.swift", `.accessibilityIdentifier("player.row.\(id)")`), []string{`"player.row.…"`})
}

func TestPlanNeverRunsUITests(t *testing.T) {
	repo := memRepo{
		"api/go.mod":               "module example.com/api",
		"api/internal/a/a_test.go": "package a",
		"api/a_test.go":            "package api",
		"pipeline/tests/test_x.py": "",
		"pipeline/tests/test_y.py": "",
		"web/src/A.test.tsx":       "",
		"apps/android/player/src/test/kotlin/a/HomeScreenTest.kt":     "package com.x.player.home\n",
		"apps/android/core/src/test/kotlin/CoreTest.kt":               "package com.x.core\n",
		"apps/android/core/src/androidTest/kotlin/CoreUiTest.kt":      "package com.x.core\n",
		"apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveTests.swift": "",
		"apps/apple/UITests/PlayerHomeUITests.swift":                  "",
		"apps/apple/UITests/ConnectUITests.swift":                     "",
		"apps/apple/PlayerMacTests/UI/PlayerMacMenuUITests.swift":     "",
		"apps/apple/PlayerMacTests/PlayerMenuTests.swift":             "",
		"apps/tv/tvos/MaktabaTVUITests/TVLiveUITests.swift":           "",
	}
	idx := newFileIndex(repo.Files())
	a := &Analysis{Hits: map[string]*Hit{}}
	for _, f := range repo.Files() {
		if idx.class(f).Kind == KindTest {
			a.Hits[f] = &Hit{File: f, Platform: idx.class(f).Platform, Via: map[string]bool{ViaNaming: true}, Selectors: map[string]bool{}}
		}
	}
	a.Hits["pipeline/tests/test_x.py"].Via = map[string]bool{ViaMap: true}
	a.Hits["pipeline/tests/test_x.py"].Selectors = map[string]bool{"test_one": true}
	env := Env{GOOS: "darwin", Has: func(string) bool { return true }, Exists: func(string) bool { return true },
		Getenv: func(string) string { return "" }, JavaHome: "/jdk17"}
	steps := plan(a, repo, env)

	byTitle := map[string]Step{}
	for _, s := range steps {
		byTitle[s.Title] = s
		if err := safeToRun(s); err != nil {
			t.Errorf("plan made an unsafe step: %v", err)
		}
		if len(s.Args) > 0 && s.Args[0] == "xcodebuild" {
			action := s.Args[1]
			scheme := ""
			for i, a := range s.Args {
				if a == "-scheme" {
					scheme = s.Args[i+1]
				}
			}
			if action == "test" && scheme != "MaktabaKit" {
				t.Errorf("runs tests of %s", scheme)
			}
			if action == "build-for-testing" && !contains(s.Args, "CODE_SIGNING_ALLOWED=NO") {
				t.Errorf("%s builds signed", scheme)
			}
		}
	}
	goStep := byTitle["Go api"]
	if !reflect.DeepEqual(goStep.Args, []string{"go", "test", "-short", "-count=1", ".", "./internal/a"}) &&
		!reflect.DeepEqual(goStep.Args, []string{"go", "test", "-short", "-count=1", "./internal/a", "."}) {
		t.Errorf("go step = %v", goStep.Args)
	}
	if !contains(goStep.Env, "CGO_ENABLED=0") {
		t.Errorf("go step env = %v", goStep.Env)
	}
	py := byTitle["Python pipeline"]
	if !contains(py.Args, "tests/test_x.py::test_one") || !contains(py.Args, "tests/test_y.py") {
		t.Errorf("python step = %v", py.Args)
	}
	android := byTitle[platformTitle[PAndroid]]
	joined := strings.Join(android.Args, " ")
	if !strings.Contains(joined, ":player:testPhoneDebugUnitTest --tests com.x.player.home.HomeScreenTest") ||
		!strings.Contains(joined, ":core:testDebugUnitTest --tests com.x.core.CoreTest") || !contains(android.Env, "JAVA_HOME=/jdk17") {
		t.Errorf("android step = %v %v", android.Args, android.Env)
	}
	if s := byTitle[platformTitle[PAndroidInstr]]; s.Skip == "" {
		t.Error("instrumented tests not skipped")
	}
	var builds []string
	for _, s := range steps {
		if s.Compile {
			builds = append(builds, s.Args[5])
		}
	}
	sort.Strings(builds)
	if !reflect.DeepEqual(builds, []string{"Maktaba Player", "Maktaba Player Mac", "Maktaba Remote", "Maktaba TV"}) {
		t.Errorf("builds = %v", builds)
	}

	// no Xcode (Linux): Apple steps are skipped, not run
	env.GOOS = "linux"
	for _, s := range plan(a, repo, env) {
		if len(s.Args) > 0 && s.Args[0] == "xcodebuild" && s.Skip == "" {
			t.Errorf("xcodebuild step without Xcode: %v", s.Args)
		}
	}
}

func TestSafeToRun(t *testing.T) {
	refuse := [][]string{
		{"xcodebuild", "test", "-scheme", "Maktaba Player Mac", "-destination", "platform=macOS"},
		{"xcodebuild", "test-without-building", "-scheme", "Maktaba Player"},
		{"xcodebuild", "-scheme", "Maktaba Player", "test"},
		{"xcodebuild", "build", "-destination", "id=$(xcrun simctl create x)"},
	}
	for _, args := range refuse {
		if safeToRun(Step{Args: args}) == nil {
			t.Errorf("safeToRun(%v) allowed it", args)
		}
	}
	allow := [][]string{
		{"xcodebuild", "test", "-scheme", "MaktabaKit", "-destination", "platform=macOS"},
		{"xcodebuild", "build-for-testing", "-scheme", "Maktaba Player Mac", "CODE_SIGNING_ALLOWED=NO"},
		{"go", "test", "./..."},
	}
	for _, args := range allow {
		if err := safeToRun(Step{Args: args}); err != nil {
			t.Errorf("safeToRun(%v): %v", args, err)
		}
	}
}

func TestBuildMap(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) string {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("api/go.mod", "module github.com/x/repo/api\n\ngo 1.23\n")
	files := []string{
		"api/go.mod", "api/internal/a/a.go", "api/internal/b/b.go", "api/internal/b/b_test.go", "api/internal/c/c.go",
		"pipeline/src/maktaba_pipeline/x.py", "pipeline/tests/test_x.py",
		"apps/apple/MaktabaKit/Sources/MaktabaKit/Live.swift", "apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveTests.swift",
		"apps/android/player/src/main/kotlin/com/x/player/Home.kt", "apps/android/core/src/main/kotlin/com/x/player/Home.kt",
	}
	write("cov/go/b.cover", "mode: set\n"+
		"github.com/x/repo/api/internal/a/a.go:3.1,4.2 1 1\n"+
		"github.com/x/repo/api/internal/a/a.go:5.1,6.2 1 0\n"+
		"github.com/x/repo/api/internal/c/c.go:3.1,4.2 1 0\n"+
		"github.com/x/repo/api/internal/b/b.go:3.1,4.2 2 3\n")
	write("cov/go/index.tsv", "api/internal/b\tb.cover\napi/internal/missing\tnone.cover\n")
	write("cov/py.json", `{"files":{"src/maktaba_pipeline/x.py":{"contexts":{"1":[""],"3":["tests/test_x.py::test_a|run","tests/test_x.py::TestK::test_b[p1]|setup"]}}}}`)
	write("frag/kit.xccov.json", `{"targets":[{"name":"MaktabaKit","files":[
		{"path":"/Users/runner/work/Maktaba/Maktaba/apps/apple/MaktabaKit/Sources/MaktabaKit/Live.swift","coveredLines":10},
		{"path":"/Users/runner/work/Maktaba/Maktaba/apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveTests.swift","coveredLines":9}]}]}`)
	write("frag/kit.xccov.json.id", "apple-kit:apps/apple/MaktabaKit/Tests/MaktabaKitTests\n")
	write("frag/player.jacoco.xml", `<?xml version="1.0"?><!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd">
<report name="player"><package name="com/x/player"><sourcefile name="Home.kt"><line nr="1" mi="0" ci="2"/>
<counter type="LINE" missed="1" covered="4"/></sourcefile><sourcefile name="Unused.kt"><counter type="LINE" missed="3" covered="0"/></sourcefile></package></report>`)
	write("frag/player.jacoco.xml.id", "android:apps/android/player/src/test")
	prev := newMap()
	ps := newSectionBuilder("old")
	ps.add("apps/android/player/src/main/kotlin/com/x/player/Home.kt", "android-tv:old")
	ps.add("api/internal/gone.go", "go:api/old") // a file no longer in the tree
	prev.Sections["android-tv:apps/tv/android/app/src/test"] = ps.done()
	prev.Sections["go"] = ps.done()
	if err := prev.save(filepath.Join(dir, "prev.json")); err != nil {
		t.Fatal(err)
	}

	m, notes, err := buildMap(MapOptions{
		Root: dir, Previous: filepath.Join(dir, "prev.json"), GoDir: filepath.Join(dir, "cov/go"),
		PyJSON: filepath.Join(dir, "cov/py.json"), PyRoot: "pipeline", Fragments: filepath.Join(dir, "frag"),
		Commit: "abc123", Generated: "2026-11-01T00:00:00Z",
	}, files)
	if err != nil || len(notes) != 0 {
		t.Fatalf("buildMap: %v %v", err, notes)
	}
	want := map[string][]string{
		"api/internal/a/a.go":                                         {"go:api/internal/b"},
		"api/internal/b/b.go":                                         {"go:api/internal/b"},
		"api/internal/c/c.go":                                         nil, // never run
		"pipeline/src/maktaba_pipeline/x.py":                          {"python:pipeline/tests/test_x.py::TestK::test_b", "python:pipeline/tests/test_x.py::test_a"},
		"apps/apple/MaktabaKit/Sources/MaktabaKit/Live.swift":         {"apple-kit:apps/apple/MaktabaKit/Tests/MaktabaKitTests"},
		"apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveTests.swift": nil, // a test is not a source
		"apps/android/player/src/main/kotlin/com/x/player/Home.kt":    {"android-tv:old", "android:apps/android/player/src/test"},
		"apps/android/core/src/main/kotlin/com/x/player/Home.kt":      nil, // same package, other module
		"api/internal/gone.go":                                        nil,
	}
	for src, ids := range want {
		got := m.TestsFor(src)
		sort.Strings(got)
		if !reflect.DeepEqual(got, ids) && (len(got) != 0 || len(ids) != 0) {
			t.Errorf("TestsFor(%s) = %v, want %v", src, got, ids)
		}
	}
	if m.Sections["go"].Commit != "abc123" || m.Sections["android-tv:apps/tv/android/app/src/test"] == nil {
		t.Errorf("sections: go measured %q; kept android-tv: %v", m.Sections["go"].Commit, m.Sections["android-tv:apps/tv/android/app/src/test"] != nil)
	}
}

func TestPythonTestID(t *testing.T) {
	for ctx, want := range map[string]string{
		"tests/a/test_b.py::test_c|run":           "python:pipeline/tests/a/test_b.py::test_c",
		"tests/a/test_b.py::K::test_c[1-2]|setup": "python:pipeline/tests/a/test_b.py::K::test_c",
		"":                  "",
		"tests/a/test_b.py": "",
	} {
		if got := pythonTestID(ctx, "pipeline"); got != want {
			t.Errorf("pythonTestID(%q) = %q, want %q", ctx, got, want)
		}
	}
}

func zipOf(t *testing.T, name, content string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(content))
	_ = zw.Close()
	return buf.Bytes()
}

func TestFetchMap(t *testing.T) {
	mapJSON := `{"version":1,"sections":{}}`
	var calls []string
	gh := func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch {
		case strings.Contains(args[1], "artifacts?name="):
			list := map[string]any{"artifacts": []map[string]any{
				{"id": 1, "expired": true, "workflow_run": map[string]any{"head_branch": "main"}},
				{"id": 2, "expired": false, "workflow_run": map[string]any{"head_branch": "feature"}},
				{"id": 3, "expired": false, "created_at": "2026-11-01T00:00:00Z", "workflow_run": map[string]any{"head_branch": "main", "head_sha": "0123456789abcdef"}},
			}}
			return json.Marshal(list)
		case strings.HasSuffix(args[1], "/artifacts/3/zip"):
			return zipOf(t, mapFileName, mapJSON), nil
		}
		return nil, fmt.Errorf("unexpected gh %v", args)
	}
	b, from, err := fetchMap(gh, "o/r")
	if err != nil || string(b) != mapJSON {
		t.Fatalf("fetchMap = %q, %v (calls %v)", b, err, calls)
	}
	if !strings.Contains(from, "main@012345678") {
		t.Errorf("from = %q", from)
	}
	if _, _, err := fetchMap(func(...string) ([]byte, error) { return []byte(`{"artifacts":[]}`), nil }, "o/r"); err == nil {
		t.Error("no artifacts: no error")
	}
	if _, err := unzipFile(zipOf(t, "other.json", "{}"), mapFileName); err == nil {
		t.Error("zip without the map: no error")
	}
}

func TestRepoSlug(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:Hamza-Labs-Core/Maktaba.git":      "Hamza-Labs-Core/Maktaba",
		"https://github.com/Hamza-Labs-Core/Maktaba":      "Hamza-Labs-Core/Maktaba",
		"https://github.com/Hamza-Labs-Core/Maktaba.git/": "Hamza-Labs-Core/Maktaba",
	} {
		m := reRemote.FindStringSubmatch(url)
		if m == nil || m[1] != want {
			t.Errorf("%s → %v, want %s", url, m, want)
		}
	}
}

func TestObtainMapFromFileAndNone(t *testing.T) {
	if m, note := obtainMap(".", "none", true, nil, time.Now()); m != nil || !strings.Contains(note, "searches only") {
		t.Errorf("none: %v %q", m, note)
	}
	file := filepath.Join(t.TempDir(), "m.json")
	_ = os.WriteFile(file, []byte(`{"version":1,"sections":{"go":{"granularity":"g","tests":[],"files":{}}}}`), 0o644)
	if m, note := obtainMap(".", file, true, nil, time.Now()); m == nil || !strings.Contains(note, "sections: go") {
		t.Errorf("file: %v %q", m, note)
	}
	if m, note := obtainMap(".", file+".missing", true, nil, time.Now()); m != nil || !strings.Contains(note, "searches only") {
		t.Errorf("missing file: %v %q", m, note)
	}
}
