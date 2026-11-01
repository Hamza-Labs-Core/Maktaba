package main

import (
	"path"
	"regexp"
	"strings"
)

// namingTests finds a source file's tests by name: foo.go → foo_test.go, Foo.swift →
// FooTests.swift, foo.py → test_foo.py, foo.ts → foo.test.ts, Foo.kt → FooTest.kt. The
// second result is weaker kin: a Go file's package tests, an Apple screen folder's tests
// (Player/Home/… → PlayerHomeUITests).
func namingTests(src string, idx *fileIndex) (named, kin []string) {
	c := idx.class(src)
	dir, base := path.Dir(src), path.Base(src)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch c.Family {
	case familyGo:
		for _, f := range idx.testsUnder(dir + "/") {
			if path.Dir(f) != dir {
				continue
			}
			fb := strings.TrimSuffix(path.Base(f), "_test.go")
			if fb == stem || strings.HasPrefix(fb, stem+"_") {
				named = append(named, f)
			} else {
				kin = append(kin, f)
			}
		}

	case familyPython:
		if stem == "__init__" || stem == "__main__" {
			return nil, nil
		}
		parent := path.Base(dir)
		var all, near []string
		for _, f := range idx.byFam[familyPython] {
			fb := strings.TrimSuffix(path.Base(f), ".py")
			if fb == "test_"+stem || strings.HasPrefix(fb, "test_"+stem+"_") {
				all = append(all, f)
				if strings.Contains(f, "/"+parent+"/") {
					near = append(near, f)
				}
			}
		}
		// several test_models.py: the ones in a folder named like the source's package
		if len(near) > 0 {
			return near, nil
		}
		return all, nil

	case familyWeb:
		for _, f := range idx.testsUnder(dir + "/") {
			fd := path.Dir(f)
			if fd != dir && fd != dir+"/__tests__" {
				continue
			}
			fb := path.Base(f)
			if strings.HasPrefix(fb, stem+".test.") || strings.HasPrefix(fb, stem+".spec.") ||
				strings.HasPrefix(fb, stem+".") && (strings.Contains(fb, ".test.") || strings.Contains(fb, ".spec.")) {
				named = append(named, f)
			}
		}

	case familyAndroid, familyAndroidTV, familyDesktopPl:
		re := regexp.MustCompile(`^` + regexp.QuoteMeta(stem) + `(?:[A-Z][A-Za-z0-9]*)?Tests?\.(?:kt|java)$`)
		for _, fam := range familyTests[c.Family] {
			for _, f := range idx.byFam[fam] {
				if re.MatchString(path.Base(f)) {
					named = append(named, f)
				}
			}
		}

	case familyApple:
		re := regexp.MustCompile(`^` + regexp.QuoteMeta(stem) + `(?:[A-Z][A-Za-z0-9]*)?Tests?\.swift$`)
		for _, f := range idx.byFam[familyApple] {
			if re.MatchString(path.Base(f)) {
				named = append(named, f)
			}
		}
		// the app's screen folders: apps/apple/Player/Home/HomeScreen.swift → PlayerHomeUITests
		if app, folder, ok := appleScreenFolder(src); ok {
			fre := regexp.MustCompile(`^(?:` + app + `)?(?:Mac)?` + regexp.QuoteMeta(folder) + `(?:UI)?Tests?\.swift$`)
			for _, f := range idx.byFam[familyApple] {
				if fre.MatchString(path.Base(f)) && !contains(named, f) {
					kin = append(kin, f)
				}
			}
		}

	case familyRust:
		// tests live in the file itself (#[cfg(test)]); the gate reads them there
		for _, f := range idx.testsUnder("apps/desktop/src-tauri/tests/") {
			if strings.TrimSuffix(path.Base(f), ".rs") == stem {
				named = append(named, f)
			}
		}

	case familyScripts:
		for _, cand := range []string{stem + "-test" + ext, stem + "_test" + ext, "test_" + stem + ext, "test-" + stem + ext} {
			if f := path.Join(dir, cand); idx.has(f) {
				named = append(named, f)
			}
		}
		// tools/apple/check-string-catalogs.py ↔ tools/apple/test_check_string_catalogs.py
		if ext == ".py" {
			if f := path.Join(dir, "test_"+strings.ReplaceAll(stem, "-", "_")+ext); idx.has(f) && !contains(named, f) {
				named = append(named, f)
			}
		}
	}
	return named, kin
}

// appleScreenFolder: apps/apple/Player/Home/HomeScreen.swift → ("Player", "Home").
func appleScreenFolder(src string) (app, folder string, ok bool) {
	rest := strings.TrimPrefix(src, "apps/apple/")
	if rest == src {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || (parts[0] != "Player" && parts[0] != "Remote") {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
