package main

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
)

// How a test was found.
const (
	ViaMap     = "map"     // the coverage map: it executes the source
	ViaNaming  = "naming"  // its name: foo.go → foo_test.go
	ViaPackage = "package" // its place: the source's Go package, its Apple screen folder
	ViaUI      = "ui"      // it uses an identifier or string of the changed file
	ViaImports = "imports" // the web: it imports the source, directly or not
	ViaInline  = "inline"  // Rust: the tests are in the file
)

// Hit is one affected test file.
type Hit struct {
	File      string
	Platform  string
	Via       map[string]bool
	Selectors map[string]bool // single tests the map names (pytest nodes); none = the whole file
	Terms     []string        // what the UI search matched
	For       map[string]bool // the changed files that led to it
}

// SourceResult is one changed source file and its tests.
type SourceResult struct {
	Path          string
	Class         Class
	Tests         []string // its affected test files
	Changed       []string // those the range changed
	Inline        bool     // tests inside the file (Rust #[cfg(test)])
	InlineChanged bool     // the range changed lines in them
	Satisfied     bool
	Uncovered     bool // no test found by any source
	// UI test platforms whose tests use an identifier or phrase of the changed lines: each
	// needs one of those tests changed (the iPhone tests updated, the Mac ones forgotten)
	UIPlatforms      []string
	MissingPlatforms []string
}

// Analysis is the whole answer for a range.
type Analysis struct {
	Changes      []Change
	Sources      []*SourceResult // changed source files the gate checks
	Hits         map[string]*Hit // every affected test file
	ChangedTests map[string]bool
	Stale        []Stale
	Other        []string // changed files that are neither source nor test
	Override     string   // a No-Test-Change trailer's reason
	MapNote      string   // where the coverage map came from, or why there is none
	Notes        []string // warnings worth printing
}

// Inputs is what analyze reads.
type Inputs struct {
	Changes  []Change
	Repo     Repo
	Map      *CoverageMap // nil: searches only
	MapNote  string
	Messages []string // the range's commit messages
}

// Blocked is the gate's decision.
func (a *Analysis) Blocked() bool {
	if a.Override != "" {
		return false
	}
	if len(a.Stale) > 0 {
		return true
	}
	for _, s := range a.Sources {
		if !s.Satisfied {
			return true
		}
	}
	return false
}

var reTrailer = regexp.MustCompile(`(?mi)^No-Test-Change:[ \t]*(\S.*?)\s*$`)

// overrideReason returns the first No-Test-Change trailer's reason in the messages.
func overrideReason(msgs []string) string {
	for _, m := range msgs {
		if r := reTrailer.FindStringSubmatch(m); r != nil {
			return r[1]
		}
	}
	return ""
}

func analyze(in Inputs) *Analysis {
	idx := newFileIndex(in.Repo.Files())
	a := &Analysis{
		Changes:      in.Changes,
		Hits:         map[string]*Hit{},
		ChangedTests: map[string]bool{},
		MapNote:      in.MapNote,
		Override:     overrideReason(in.Messages),
	}
	for _, ch := range in.Changes {
		if ch.Status != 'D' && idx.class(ch.Path).Kind == KindTest {
			a.ChangedTests[ch.Path] = true
		}
	}

	var tb *tables
	var graph *webGraph
	addedSwift := false
	pbxChanged := false

	for _, ch := range in.Changes {
		c := idx.class(ch.Path)
		if ch.Path == "apps/apple/Maktaba.xcodeproj/project.pbxproj" {
			pbxChanged = true
		}
		if ch.Status == 'A' && strings.HasPrefix(ch.Path, "apps/apple/") && strings.HasSuffix(ch.Path, ".swift") &&
			!strings.HasPrefix(ch.Path, "apps/apple/MaktabaKit/") {
			addedSwift = true
		}
		var sr *SourceResult
		if c.Kind == KindSource && ch.Status != 'D' {
			sr = &SourceResult{Path: ch.Path, Class: c}
			a.Sources = append(a.Sources, sr)
		} else if c.Kind != KindTest {
			a.Other = append(a.Other, ch.Path)
		}
		found := map[string]bool{}
		add := func(file, via string, selector string, terms []string) {
			if !idx.has(file) {
				return
			}
			h := a.Hits[file]
			if h == nil {
				h = &Hit{File: file, Platform: idx.class(file).Platform, Via: map[string]bool{},
					Selectors: map[string]bool{}, For: map[string]bool{}}
				a.Hits[file] = h
			}
			h.Via[via] = true
			h.For[ch.Path] = true
			if selector != "" {
				h.Selectors[selector] = true
			}
			for _, t := range terms {
				if !contains(h.Terms, t) {
					h.Terms = append(h.Terms, t)
				}
			}
			found[file] = true
		}

		if c.Kind == KindSource && ch.Status != 'D' {
			// a: the coverage map
			for _, id := range in.Map.TestsFor(ch.Path) {
				t, ok := parseTestID(id)
				if !ok {
					continue
				}
				for _, f := range resolveTestID(t, idx) {
					add(f, ViaMap, t.Selector, nil)
				}
			}
			// c: naming
			named, kin := namingTests(ch.Path, idx)
			for _, f := range named {
				add(f, ViaNaming, "", nil)
			}
			for _, f := range kin {
				add(f, ViaPackage, "", nil)
			}
			// the web's import graph
			if c.Family == familyWeb {
				if graph == nil {
					graph = buildWebGraph(in.Repo, idx)
				}
				for _, f := range graph.testsOf(ch.Path, idx) {
					add(f, ViaImports, "", nil)
				}
			}
			// Rust: tests inside the file
			if c.Family == familyRust {
				if b, err := in.Repo.Read(ch.Path); err == nil {
					if line := cfgTestLine(b); line > 0 {
						sr.Inline = true
						for _, l := range ch.Added {
							if l.N > line {
								sr.InlineChanged = true
							}
						}
					}
				}
			}
		}
		// b: the UI search (sources, and the string tables)
		strictPlatforms := map[string]bool{}
		if uiSearchable(ch.Path, c) {
			if tb == nil {
				tb = &tables{en: loadEnglish(in.Repo), swift: loadSwiftTables(in.Repo, idx)}
			}
			ui := uiSearch(ch, c, in.Repo, idx, *tb)
			for f, terms := range ui.Hits {
				add(f, ViaUI, "", terms)
			}
			for f := range ui.Strict {
				strictPlatforms[idx.class(f).Platform] = true
			}
			a.Stale = append(a.Stale, ui.Stale...)
		}

		if sr != nil {
			for f := range found {
				sr.Tests = append(sr.Tests, f)
				if a.ChangedTests[f] {
					sr.Changed = append(sr.Changed, f)
				}
			}
			sort.Strings(sr.Tests)
			sort.Strings(sr.Changed)
			sr.UIPlatforms = sortedSet(strictPlatforms)
		}
	}

	// the gate: each source needs one of its tests changed in the range
	for _, sr := range a.Sources {
		for _, p := range sr.UIPlatforms {
			changed := false
			for _, f := range sr.Changed {
				if idx.class(f).Platform == p {
					changed = true
				}
			}
			if !changed {
				sr.MissingPlatforms = append(sr.MissingPlatforms, p)
			}
		}
		switch {
		case len(sr.MissingPlatforms) > 0:
			sr.Satisfied = false
		case len(sr.Changed) > 0 || sr.InlineChanged:
			sr.Satisfied = true
		case len(sr.Tests) == 0 && !sr.Inline:
			// nothing found covers it: a new or changed test of its project will do
			sr.Uncovered = true
			for f := range a.ChangedTests {
				fc := idx.class(f)
				if sr.Class.Family == familyGo {
					if fc.Family == familyGo && fc.Project == sr.Class.Project {
						sr.Satisfied = true
					}
				} else if contains(familyTests[sr.Class.Family], fc.Family) {
					sr.Satisfied = true
				}
			}
		}
	}
	a.Stale = dedupStale(a.Stale, a.ChangedTests)
	if addedSwift && !pbxChanged {
		a.Notes = append(a.Notes, "new Swift files under apps/apple but Maktaba.xcodeproj did not change: run `xcodegen generate` in apps/apple, or the new files (and tests) are not built")
	}
	return a
}

// dedupStale drops repeats. A stale test the range changed still counts: it still uses
// the term at the head.
func dedupStale(st []Stale, _ map[string]bool) []Stale {
	seen := map[Stale]bool{}
	var out []Stale
	for _, s := range st {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Test != out[j].Test {
			return out[i].Test < out[j].Test
		}
		return out[i].Term < out[j].Term
	})
	return out
}

// cfgTestLine is the line of a Rust file's #[cfg(test)], or 0.
func cfgTestLine(b []byte) int {
	i := bytes.Index(b, []byte("#[cfg(test)]"))
	if i < 0 {
		return 0
	}
	return bytes.Count(b[:i], []byte("\n")) + 1
}
