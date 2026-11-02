package main

import (
	"path"
	"sort"
	"strings"
)

// Kind is what a file is to the gate.
type Kind int

const (
	KindOther       Kind = iota // config, docs, assets, scripts: not gated
	KindSource                  // app or service code: its tests must change with it
	KindTest                    // a test file
	KindTestSupport             // in a test folder, not a test itself (fixtures, helpers)
)

// Test platforms: how a test runs, and the heading it is listed under.
const (
	PGo             = "go"
	PPython         = "python"
	PWeb            = "web"
	PAndroid        = "android"              // apps/android JVM unit tests (src/test)
	PAndroidInstr   = "android-instrumented" // apps/android src/androidTest
	PAndroidTV      = "android-tv"           // apps/tv/android JVM unit tests
	PAndroidTVInstr = "android-tv-instrumented"
	PDesktopPlayer  = "desktop-player" // apps/desktop-player (Kotlin JVM)
	PAppleKit       = "apple-kit"      // MaktabaKit's unit tests (macOS, no host app)
	PAppleIOSUI     = "apple-ios-ui"   // apps/apple/UITests: iPhone and iPad UI tests
	PAppleMac       = "apple-mac"      // apps/apple/PlayerMacTests: the Mac app's hosted unit tests
	PAppleMacUI     = "apple-mac-ui"   // apps/apple/PlayerMacTests/UI: Mac UI tests
	PTVOSUI         = "tvos-ui"        // apps/tv/tvos/MaktabaTVUITests
	PRust           = "rust"           // apps/desktop/src-tauri (tests inline)
	PScripts        = "scripts"        // tools/ and other scripts with a *-test / test_* pair
	PSourceApple    = "apple"          // a source platform: the Apple apps and MaktabaKit
	PSourceTVOS     = "tvos"
	familyGo        = "go"
	familyPython    = "python"
	familyWeb       = "web"
	familyAndroid   = "android"
	familyAndroidTV = "android-tv"
	familyDesktopPl = "desktop-player"
	familyApple     = "apple"
	familyRust      = "rust"
	familyScripts   = "scripts"
)

// platformTitle is the heading a test platform is listed under.
var platformTitle = map[string]string{
	PGo:             "Go",
	PPython:         "Python pipeline",
	PWeb:            "Web (vitest)",
	PAndroid:        "Android Player/Remote (JVM unit)",
	PAndroidInstr:   "Android Player/Remote (instrumented, CI only)",
	PAndroidTV:      "Android TV (JVM unit)",
	PAndroidTVInstr: "Android TV (instrumented, CI only)",
	PDesktopPlayer:  "Desktop Player (JVM)",
	PAppleKit:       "Apple MaktabaKit (unit)",
	PAppleIOSUI:     "Apple iPhone/iPad UITests",
	PAppleMac:       "Apple Mac tests (app-hosted)",
	PAppleMacUI:     "Apple Mac UI tests",
	PTVOSUI:         "Apple TV UITests",
	PRust:           "Rust desktop shell",
	PScripts:        "Scripts",
}

// platformOrder is the order platforms are listed in.
var platformOrder = []string{PGo, PPython, PWeb, PAndroid, PAndroidInstr, PAndroidTV, PAndroidTVInstr,
	PDesktopPlayer, PAppleKit, PAppleIOSUI, PAppleMac, PAppleMacUI, PTVOSUI, PRust, PScripts}

// Class is what classify says about one path.
type Class struct {
	Kind     Kind
	Platform string // a test's platform (above), or a source's language/app platform
	Family   string // the tests a source's searches look through (familyTests)
	Project  string // the project root: a Go module, "pipeline", "web", "apps/android"…
}

// Classifier knows the repo's layout: its Go modules.
type Classifier struct {
	goModules []string // module dirs, longest first
}

func newClassifier(files []string) *Classifier {
	c := &Classifier{}
	for _, f := range files {
		if path.Base(f) == "go.mod" {
			c.goModules = append(c.goModules, path.Dir(f))
		}
	}
	sort.Slice(c.goModules, func(i, j int) bool { return len(c.goModules[i]) > len(c.goModules[j]) })
	return c
}

func (c *Classifier) goModule(p string) string {
	for _, m := range c.goModules {
		if m == "." || strings.HasPrefix(p, m+"/") {
			return m
		}
	}
	return ""
}

// generated reports code no one edits by hand: its tests are the generator's.
func generated(p string) bool {
	b := path.Base(p)
	return strings.HasSuffix(b, ".pb.go") || strings.HasSuffix(b, "_gen.go") ||
		strings.HasPrefix(b, "zz_generated") || strings.HasSuffix(b, ".d.ts") ||
		strings.Contains(p, "/generated/") || b == "Tokens.swift" || b == "Tokens.kt"
}

func underAny(p string, dirs ...string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

func (c *Classifier) classify(p string) Class {
	ext := path.Ext(p)
	base := path.Base(p)
	switch {
	case strings.Contains(p, "/node_modules/"):
		return Class{}

	case ext == ".go":
		mod := c.goModule(p)
		if strings.Contains(p, "/testdata/") {
			return Class{Kind: KindTestSupport, Platform: PGo, Family: familyGo, Project: mod}
		}
		if strings.HasSuffix(base, "_test.go") {
			return Class{Kind: KindTest, Platform: PGo, Family: familyGo, Project: mod}
		}
		if generated(p) || mod == "" {
			return Class{Platform: PGo, Family: familyGo, Project: mod}
		}
		return Class{Kind: KindSource, Platform: PGo, Family: familyGo, Project: mod}

	case strings.HasPrefix(p, "pipeline/"):
		if ext != ".py" {
			return Class{}
		}
		if strings.HasPrefix(p, "pipeline/tests/") {
			if strings.HasPrefix(base, "test_") {
				return Class{Kind: KindTest, Platform: PPython, Family: familyPython, Project: "pipeline"}
			}
			return Class{Kind: KindTestSupport, Platform: PPython, Family: familyPython, Project: "pipeline"}
		}
		if strings.HasPrefix(p, "pipeline/src/") {
			return Class{Kind: KindSource, Platform: PPython, Family: familyPython, Project: "pipeline"}
		}
		return Class{Platform: PPython, Family: familyPython, Project: "pipeline"}

	case strings.HasPrefix(p, "web/"):
		if ext != ".ts" && ext != ".tsx" {
			if p == "web/src/i18n/en.json" {
				return Class{Platform: PWeb, Family: familyWeb, Project: "web"}
			}
			return Class{}
		}
		if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
			return Class{Kind: KindTest, Platform: PWeb, Family: familyWeb, Project: "web"}
		}
		if strings.HasPrefix(p, "web/src/test/") || strings.Contains(p, "/__tests__/") || strings.Contains(p, "/test-utils") {
			return Class{Kind: KindTestSupport, Platform: PWeb, Family: familyWeb, Project: "web"}
		}
		if generated(p) || !underAny(p, "web/src/", "web/cloud/src/", "web/design-system/", "web/packages/") {
			return Class{Platform: PWeb, Family: familyWeb, Project: "web"}
		}
		return Class{Kind: KindSource, Platform: PWeb, Family: familyWeb, Project: "web"}

	case strings.HasPrefix(p, "apps/android/"):
		return gradleClass(p, "apps/android", familyAndroid, PAndroid, PAndroidInstr)
	case strings.HasPrefix(p, "apps/tv/android/"):
		return gradleClass(p, "apps/tv/android", familyAndroidTV, PAndroidTV, PAndroidTVInstr)
	case strings.HasPrefix(p, "apps/desktop-player/"):
		return gradleClass(p, "apps/desktop-player", familyDesktopPl, PDesktopPlayer, PDesktopPlayer)

	case strings.HasPrefix(p, "apps/apple/MaktabaKit/"):
		if strings.HasPrefix(p, "apps/apple/MaktabaKit/Tests/") {
			if ext == ".swift" && isTestName(base) {
				return Class{Kind: KindTest, Platform: PAppleKit, Family: familyApple, Project: "apps/apple"}
			}
			return Class{Kind: KindTestSupport, Platform: PAppleKit, Family: familyApple, Project: "apps/apple"}
		}
		return appleSource(p, ext, PSourceApple)
	case strings.HasPrefix(p, "apps/apple/UITests/"):
		return appleTest(ext, base, PAppleIOSUI)
	case strings.HasPrefix(p, "apps/apple/PlayerMacTests/UI/"):
		return appleTest(ext, base, PAppleMacUI)
	case strings.HasPrefix(p, "apps/apple/PlayerMacTests/"):
		return appleTest(ext, base, PAppleMac)
	case strings.HasPrefix(p, "apps/apple/"):
		return appleSource(p, ext, PSourceApple)
	case strings.HasPrefix(p, "apps/tv/tvos/MaktabaTVUITests/"):
		return appleTest(ext, base, PTVOSUI)
	case strings.HasPrefix(p, "apps/tv/tvos/"):
		return appleSource(p, ext, PSourceTVOS)

	case strings.HasPrefix(p, "apps/desktop/src-tauri/"):
		if ext != ".rs" {
			return Class{}
		}
		if strings.HasPrefix(p, "apps/desktop/src-tauri/tests/") {
			return Class{Kind: KindTest, Platform: PRust, Family: familyRust, Project: "apps/desktop/src-tauri"}
		}
		if strings.HasPrefix(p, "apps/desktop/src-tauri/src/") {
			return Class{Kind: KindSource, Platform: PRust, Family: familyRust, Project: "apps/desktop/src-tauri"}
		}
		return Class{Platform: PRust, Family: familyRust, Project: "apps/desktop/src-tauri"}
	}

	// scripts: a test by its name, otherwise not gated
	if ext == ".sh" || ext == ".py" {
		stem := strings.TrimSuffix(base, ext)
		if strings.HasPrefix(stem, "test_") || strings.HasPrefix(stem, "test-") ||
			strings.HasSuffix(stem, "-test") || strings.HasSuffix(stem, "_test") {
			return Class{Kind: KindTest, Platform: PScripts, Family: familyScripts, Project: path.Dir(p)}
		}
		return Class{Platform: PScripts, Family: familyScripts, Project: path.Dir(p)}
	}
	return Class{}
}

func isTestName(base string) bool {
	stem := strings.TrimSuffix(base, path.Ext(base))
	return strings.HasSuffix(stem, "Tests") || strings.HasSuffix(stem, "Test")
}

func appleTest(ext, base, platform string) Class {
	if ext == ".swift" && isTestName(base) {
		return Class{Kind: KindTest, Platform: platform, Family: familyApple, Project: "apps/apple"}
	}
	return Class{Kind: KindTestSupport, Platform: platform, Family: familyApple, Project: "apps/apple"}
}

func appleSource(p, ext, platform string) Class {
	project := "apps/apple"
	if platform == PSourceTVOS {
		project = "apps/tv/tvos"
	}
	switch {
	case ext == ".swift" && !generated(p):
		return Class{Kind: KindSource, Platform: platform, Family: familyApple, Project: project}
	case ext == ".xcstrings":
		// a catalog's strings feed the UI search; its own edits are not gated
		return Class{Platform: platform, Family: familyApple, Project: project}
	}
	return Class{}
}

// gradleClass sorts a file of a Gradle project: <root>/<module>/src/<set>/…
func gradleClass(p, root, family, unit, instr string) Class {
	rel := strings.TrimPrefix(p, root+"/")
	parts := strings.Split(rel, "/")
	// apps/desktop-player has no modules: src/ is at its root
	var set string
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "src" {
			set = parts[i+1]
			break
		}
	}
	ext := path.Ext(p)
	if set == "" {
		return Class{}
	}
	if ext != ".kt" && ext != ".java" {
		if ext == ".xml" && strings.Contains(p, "/res/values/") {
			// strings.xml and friends: UI search input, not gated
			return Class{Platform: unit, Family: family, Project: root}
		}
		return Class{}
	}
	switch set {
	case "test", "testDebug", "testRelease":
		if isTestName(path.Base(p)) {
			return Class{Kind: KindTest, Platform: unit, Family: family, Project: root}
		}
		return Class{Kind: KindTestSupport, Platform: unit, Family: family, Project: root}
	case "androidTest":
		if isTestName(path.Base(p)) {
			return Class{Kind: KindTest, Platform: instr, Family: family, Project: root}
		}
		return Class{Kind: KindTestSupport, Platform: instr, Family: family, Project: root}
	}
	if generated(p) {
		return Class{Platform: unit, Family: family, Project: root}
	}
	return Class{Kind: KindSource, Platform: unit, Family: family, Project: root}
}

// familyTests is which families' tests a source's searches (UI strings, names) look in:
// the Android client and core feed Android TV and the desktop Player too.
var familyTests = map[string][]string{
	familyGo:        {familyGo},
	familyPython:    {familyPython},
	familyWeb:       {familyWeb},
	familyAndroid:   {familyAndroid, familyAndroidTV, familyDesktopPl},
	familyAndroidTV: {familyAndroidTV},
	familyDesktopPl: {familyDesktopPl},
	familyApple:     {familyApple},
	familyRust:      {familyRust},
	familyScripts:   {familyScripts},
}

// gradleModule is a Gradle source or test file's module: "apps/android/player" for
// apps/android/player/src/…, the project itself for apps/desktop-player/src/….
func gradleModule(p string) string {
	i := strings.Index(p, "/src/")
	if i < 0 {
		return ""
	}
	return p[:i]
}
