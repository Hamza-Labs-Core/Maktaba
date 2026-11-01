package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Step is one command of the local run.
type Step struct {
	Title   string
	Dir     string // relative to the repo root
	Args    []string
	Env     []string
	OKCodes []int  // exit codes besides 0 that pass (pytest's 5: nothing selected)
	Skip    string // why this step does not run here (printed instead)
	Compile bool   // builds only (Apple UI test targets): never runs a test
}

// Env is what the plan may look at on this machine.
type Env struct {
	Root     string
	GOOS     string
	Has      func(bin string) bool
	Exists   func(rel string) bool // a path under the root
	Getenv   func(string) string
	JavaHome string // a JDK 17 home found on this machine, if JAVA_HOME is unset
	SDK      string // the Android SDK in its default place, if there is one
}

func hostEnv(root string) Env {
	e := Env{
		Root: root,
		GOOS: runtime.GOOS,
		Has: func(bin string) bool {
			_, err := exec.LookPath(bin)
			return err == nil
		},
		Exists: func(rel string) bool {
			_, err := os.Stat(filepath.Join(root, rel))
			return err == nil
		},
		Getenv: os.Getenv,
	}
	for _, home := range []string{
		"/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home",
		"/opt/homebrew/opt/openjdk@17",
		"/usr/local/opt/openjdk@17",
	} {
		if _, err := os.Stat(filepath.Join(home, "bin", "java")); err == nil {
			e.JavaHome = home
			break
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, sdk := range []string{filepath.Join(home, "Library", "Android", "sdk"), filepath.Join(home, "Android", "Sdk")} {
			if _, err := os.Stat(filepath.Join(sdk, "platforms")); err == nil {
				e.SDK = sdk
				break
			}
		}
	}
	return e
}

var rePackage = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z0-9_.]+)`)

// androidTask is the unit-test task of a Gradle module, as CI runs it.
func androidTask(module string) string {
	switch module {
	case "apps/android/client":
		return ":client:test"
	case "apps/android/core":
		return ":core:testDebugUnitTest"
	case "apps/android/player":
		return ":player:testPhoneDebugUnitTest"
	case "apps/android/remote":
		return ":remote:testPhoneDebugUnitTest"
	case "apps/tv/android/app":
		return ":app:testDebugUnitTest"
	case "apps/desktop-player":
		return "test"
	}
	return ":" + path.Base(module) + ":testDebugUnitTest"
}

// plan turns the affected tests into the commands that run (or only build) them here.
// It never runs a UI test or anything on a simulator: Apple UI test targets are built
// with build-for-testing, unsigned, and nothing else.
func plan(a *Analysis, repo Repo, env Env) []Step {
	groups := map[string][]*Hit{}
	for _, h := range a.Hits {
		groups[h.Platform] = append(groups[h.Platform], h)
	}
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool { return g[i].File < g[j].File })
	}
	var steps []Step
	darwin := env.GOOS == "darwin"
	xcode := darwin && env.Has("xcodebuild")

	// Go: the affected packages, per module
	if g := groups[PGo]; len(g) > 0 {
		idx := newClassifier(repo.Files())
		mods := map[string][]string{}
		for _, h := range g {
			mod := idx.goModule(h.File)
			if mod == "" {
				continue
			}
			pkg := "./" + strings.TrimPrefix(path.Dir(h.File), mod+"/")
			if path.Dir(h.File) == mod {
				pkg = "."
			}
			if !contains(mods[mod], pkg) {
				mods[mod] = append(mods[mod], pkg)
			}
		}
		for _, mod := range sortedKeys(mods) {
			var e []string
			if darwin {
				e = append(e, "CGO_ENABLED=0") // the cgo link fails with the macOS 27 SDK
			}
			steps = append(steps, Step{Title: "Go " + mod, Dir: mod, Env: e,
				Args: append([]string{"go", "test", "-short", "-count=1"}, mods[mod]...)})
		}
	}

	// Python: the map's single tests where that is all it named, else whole files
	if g := groups[PPython]; len(g) > 0 {
		var items []string
		for _, h := range g {
			rel := strings.TrimPrefix(h.File, "pipeline/")
			if len(h.Selectors) > 0 && len(h.Via) == 1 && h.Via[ViaMap] {
				for _, s := range sortedSet(h.Selectors) {
					items = append(items, rel+"::"+s)
				}
				continue
			}
			items = append(items, rel)
		}
		s := Step{Title: "Python pipeline", Dir: "pipeline", OKCodes: []int{5},
			Args: append([]string{"uv", "run", "--extra", "dev", "pytest", "-q", "-m", "unit", "-p", "no:cacheprovider"}, items...)}
		if !env.Has("uv") {
			s.Skip = "uv is not installed"
		}
		steps = append(steps, s)
	}

	// Web: vitest on the test files
	if g := groups[PWeb]; len(g) > 0 {
		args := []string{"pnpm", "exec", "vitest", "run"}
		for _, h := range g {
			args = append(args, strings.TrimPrefix(h.File, "web/"))
		}
		s := Step{Title: "Web (vitest)", Dir: "web", Args: args}
		if !env.Exists("web/node_modules") {
			s.Skip = "web/node_modules is missing: run `pnpm install` in web/"
		} else if !env.Has("pnpm") {
			s.Skip = "pnpm is not installed"
		}
		steps = append(steps, s)
	}

	// Android, Android TV, the desktop Player: JVM unit tests by class, one Gradle call each
	for _, p := range []struct{ platform, root string }{
		{PAndroid, "apps/android"}, {PAndroidTV, "apps/tv/android"}, {PDesktopPlayer, "apps/desktop-player"},
	} {
		g := groups[p.platform]
		if len(g) == 0 {
			continue
		}
		byTask := map[string][]string{}
		for _, h := range g {
			task := androidTask(gradleModule(h.File))
			cls := strings.TrimSuffix(path.Base(h.File), path.Ext(h.File))
			if b, err := repo.Read(h.File); err == nil {
				if m := rePackage.FindSubmatch(b); m != nil {
					cls = string(m[1]) + "." + cls
				}
			}
			if !contains(byTask[task], cls) {
				byTask[task] = append(byTask[task], cls)
			}
		}
		args := []string{"./gradlew", "--quiet"}
		for _, task := range sortedKeys(byTask) {
			args = append(args, task)
			for _, cls := range byTask[task] {
				args = append(args, "--tests", cls)
			}
		}
		s := Step{Title: platformTitle[p.platform], Dir: p.root, Args: args}
		if env.Getenv("JAVA_HOME") == "" && env.JavaHome != "" {
			s.Env = append(s.Env, "JAVA_HOME="+env.JavaHome)
		}
		noSDK := p.platform != PDesktopPlayer && !env.Exists(p.root+"/local.properties") &&
			env.Getenv("ANDROID_HOME") == "" && env.Getenv("ANDROID_SDK_ROOT") == ""
		if noSDK && env.SDK != "" {
			s.Env = append(s.Env, "ANDROID_HOME="+env.SDK)
			noSDK = false
		}
		if noSDK {
			s.Skip = "no Android SDK: copy local.properties (sdk.dir) into " + p.root + " or set ANDROID_HOME"
		} else if env.Getenv("JAVA_HOME") == "" && env.JavaHome == "" && !env.Has("java") {
			s.Skip = "no JDK 17: set JAVA_HOME"
		}
		steps = append(steps, s)
	}
	for _, p := range []string{PAndroidInstr, PAndroidTVInstr} {
		if g := groups[p]; len(g) > 0 {
			steps = append(steps, Step{Title: platformTitle[p], Skip: fmt.Sprintf("%d instrumented test files: they need a device, so CI runs them", len(g))})
		}
	}

	// MaktabaKit: unit tests on this Mac, no host app, no simulator
	if g := groups[PAppleKit]; len(g) > 0 {
		args := []string{"xcodebuild", "test", "-scheme", "MaktabaKit", "-destination", "platform=macOS",
			"CODE_SIGNING_ALLOWED=NO", "-quiet"}
		for _, h := range g {
			args = append(args, "-only-testing:MaktabaKitTests/"+strings.TrimSuffix(path.Base(h.File), ".swift"))
		}
		s := Step{Title: platformTitle[PAppleKit], Dir: "apps/apple/MaktabaKit", Args: args}
		if !xcode {
			s.Skip = "needs Xcode on a Mac"
		}
		steps = append(steps, s)
	}

	// Apple UI test targets and the Mac app-hosted tests: build only, never run
	type build struct{ scheme, dest string }
	var builds []build
	if g := groups[PAppleIOSUI]; len(g) > 0 {
		player, remote := false, false
		for _, h := range g {
			if strings.HasPrefix(path.Base(h.File), "Player") {
				player = true
			} else {
				remote = true
			}
		}
		if player {
			builds = append(builds, build{"Maktaba Player", "generic/platform=iOS Simulator"})
		}
		if remote {
			builds = append(builds, build{"Maktaba Remote", "generic/platform=iOS Simulator"})
		}
	}
	if len(groups[PAppleMac])+len(groups[PAppleMacUI]) > 0 {
		builds = append(builds, build{"Maktaba Player Mac", "platform=macOS"})
	}
	if len(groups[PTVOSUI]) > 0 {
		builds = append(builds, build{"Maktaba TV", "generic/platform=tvOS Simulator"})
	}
	for _, b := range builds {
		s := Step{Title: "Build for testing: " + b.scheme + " (compile only; UI tests run in CI)", Dir: "apps/apple", Compile: true,
			Args: []string{"xcodebuild", "build-for-testing", "-project", "Maktaba.xcodeproj", "-scheme", b.scheme,
				"-destination", b.dest, "CODE_SIGNING_ALLOWED=NO", "-quiet"}}
		if !xcode {
			s.Skip = "needs Xcode on a Mac"
		}
		steps = append(steps, s)
	}

	if g := groups[PRust]; len(g) > 0 {
		steps = append(steps, Step{Title: platformTitle[PRust], Skip: "cargo test runs in CI (the desktop-shell jobs); run `cargo test` in apps/desktop/src-tauri to try it here"})
	}
	if g := groups[PScripts]; len(g) > 0 {
		var fs []string
		for _, h := range g {
			fs = append(fs, h.File)
		}
		steps = append(steps, Step{Title: platformTitle[PScripts], Skip: "script tests run in CI: " + strings.Join(fs, ", ")})
	}
	return steps
}

// safeToRun refuses any step that would run a UI test or start a simulator.
func safeToRun(s Step) error {
	if len(s.Args) == 0 || s.Args[0] != "xcodebuild" {
		return nil
	}
	action := ""
	for _, a := range s.Args[1:] {
		switch a {
		case "test", "test-without-building":
			action = a
		case "build-for-testing", "build":
			if action == "" {
				action = a
			}
		}
	}
	if action == "test" || action == "test-without-building" {
		for i, a := range s.Args {
			if a == "-scheme" && i+1 < len(s.Args) && s.Args[i+1] == "MaktabaKit" {
				return nil
			}
		}
		return fmt.Errorf("refusing %q: only MaktabaKit's unit tests run locally; UI and app-hosted tests run in CI", strings.Join(s.Args, " "))
	}
	for _, a := range s.Args {
		if strings.Contains(a, "simctl") {
			return fmt.Errorf("refusing a simulator command")
		}
	}
	return nil
}

// execute runs the steps one by one and reports which failed.
func execute(steps []Step, root string, out io.Writer) (failed []string) {
	for _, s := range steps {
		if s.Skip != "" {
			fmt.Fprintf(out, "\n-- %s: skipped (%s)\n", s.Title, s.Skip)
			continue
		}
		if err := safeToRun(s); err != nil {
			fmt.Fprintf(out, "\n-- %s: %v\n", s.Title, err)
			failed = append(failed, s.Title)
			continue
		}
		fmt.Fprintf(out, "\n-- %s\n   $ (cd %s && %s%s)\n", s.Title, s.Dir, envPrefix(s.Env), shellJoin(s.Args))
		start := time.Now()
		cmd := exec.Command(s.Args[0], s.Args[1:]...)
		cmd.Dir = filepath.Join(root, s.Dir)
		cmd.Env = append(os.Environ(), s.Env...)
		cmd.Stdout, cmd.Stderr = out, out
		err := cmd.Run()
		code := 0
		if err != nil {
			code = -1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		ok := code == 0
		for _, c := range s.OKCodes {
			if code == c {
				ok = true
			}
		}
		if ok {
			fmt.Fprintf(out, "   ok (%s)\n", time.Since(start).Round(time.Second))
		} else {
			fmt.Fprintf(out, "   FAILED (exit %d, %s)\n", code, time.Since(start).Round(time.Second))
			failed = append(failed, s.Title)
		}
	}
	return failed
}

func envPrefix(env []string) string {
	if len(env) == 0 {
		return ""
	}
	return strings.Join(env, " ") + " "
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \"'$*") {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			q[i] = a
		}
	}
	return strings.Join(q, " ")
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sortedSet(m map[string]bool) []string { return sortedKeys(m) }
