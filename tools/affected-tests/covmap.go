package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// CoverageMap is the coverage map: for each source file, the tests that execute it.
//
// It is kept per section ("go", "python", "apple", "android"), each with the commit it was
// measured on, so a run that measures only some platforms (CI's Apple and Android jobs run
// only when their apps change) keeps the others from the previous map.
//
// A test ID is "<platform>:<path>[::<selector>]": path is a test file (Python, Apple) or a
// test directory (a Go package, an Apple test target, an Android module's src/test), and
// the selector narrows it to one test (a pytest node).
type CoverageMap struct {
	Version   int                    `json:"version"`
	Generated string                 `json:"generated,omitempty"`
	Commit    string                 `json:"commit,omitempty"`
	Sections  map[string]*MapSection `json:"sections"`
}

// MapSection is one platform's measurement.
type MapSection struct {
	Commit      string           `json:"commit,omitempty"`
	Generated   string           `json:"generated,omitempty"`
	Granularity string           `json:"granularity"`
	Tests       []string         `json:"tests"`
	Files       map[string][]int `json:"files"` // source path → indexes into Tests
}

const mapVersion = 1

func newMap() *CoverageMap {
	return &CoverageMap{Version: mapVersion, Sections: map[string]*MapSection{}}
}

func loadMap(file string) (*CoverageMap, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return parseMap(b)
}

func parseMap(b []byte) (*CoverageMap, error) {
	var m CoverageMap
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("coverage map: %w", err)
	}
	if m.Version != mapVersion {
		return nil, fmt.Errorf("coverage map: version %d, this tool reads %d", m.Version, mapVersion)
	}
	if m.Sections == nil {
		m.Sections = map[string]*MapSection{}
	}
	for name, s := range m.Sections {
		if s == nil {
			delete(m.Sections, name)
			continue
		}
		for f, idx := range s.Files {
			for _, i := range idx {
				if i < 0 || i >= len(s.Tests) {
					return nil, fmt.Errorf("coverage map: section %s, %s: test index %d out of range", name, f, i)
				}
			}
		}
	}
	return &m, nil
}

func (m *CoverageMap) save(file string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

// TestsFor returns the test IDs that execute a source file, and whether any section
// measured the file's platform at all.
func (m *CoverageMap) TestsFor(src string) []string {
	if m == nil {
		return nil
	}
	var ids []string
	for _, name := range m.sectionNames() {
		s := m.Sections[name]
		for _, i := range s.Files[src] {
			ids = append(ids, s.Tests[i])
		}
	}
	return ids
}

func (m *CoverageMap) sectionNames() []string {
	names := make([]string, 0, len(m.Sections))
	for n := range m.Sections {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// merge replaces m's sections with next's: what next measured wins, the rest is kept.
func (m *CoverageMap) merge(next *CoverageMap) {
	for name, s := range next.Sections {
		m.Sections[name] = s
	}
	if next.Commit != "" {
		m.Commit = next.Commit
	}
	if next.Generated != "" {
		m.Generated = next.Generated
	}
}

// sectionBuilder collects one section.
type sectionBuilder struct {
	sec   *MapSection
	index map[string]int
	seen  map[string]map[int]bool
}

func newSectionBuilder(granularity string) *sectionBuilder {
	return &sectionBuilder{
		sec:   &MapSection{Granularity: granularity, Files: map[string][]int{}},
		index: map[string]int{},
		seen:  map[string]map[int]bool{},
	}
}

func (b *sectionBuilder) add(src, testID string) {
	i, ok := b.index[testID]
	if !ok {
		i = len(b.sec.Tests)
		b.sec.Tests = append(b.sec.Tests, testID)
		b.index[testID] = i
	}
	if b.seen[src] == nil {
		b.seen[src] = map[int]bool{}
	}
	if !b.seen[src][i] {
		b.seen[src][i] = true
		b.sec.Files[src] = append(b.sec.Files[src], i)
	}
}

func (b *sectionBuilder) done() *MapSection {
	for _, idx := range b.sec.Files {
		sort.Ints(idx)
	}
	return b.sec
}

// TestID is a parsed test ID.
type TestID struct {
	Platform string
	Path     string // a test file or a test directory
	Selector string // one test in it (a pytest node), or ""
}

func parseTestID(id string) (TestID, bool) {
	i := strings.Index(id, ":")
	if i <= 0 {
		return TestID{}, false
	}
	t := TestID{Platform: id[:i], Path: id[i+1:]}
	if j := strings.Index(t.Path, "::"); j >= 0 {
		t.Path, t.Selector = t.Path[:j], t.Path[j+2:]
	}
	return t, t.Path != ""
}

func (t TestID) String() string {
	s := t.Platform + ":" + t.Path
	if t.Selector != "" {
		s += "::" + t.Selector
	}
	return s
}

// resolveTestID lists the test files a test ID stands for: the file itself, or the test
// files in its directory (a Go package's own directory only; deeper for the others).
func resolveTestID(t TestID, idx *fileIndex) []string {
	if idx.has(t.Path) {
		return []string{t.Path}
	}
	dir := strings.TrimSuffix(t.Path, "/") + "/"
	var out []string
	for _, f := range idx.testsUnder(dir) {
		if t.Platform == PGo && path.Dir(f) != strings.TrimSuffix(dir, "/") {
			continue
		}
		out = append(out, f)
	}
	return out
}
