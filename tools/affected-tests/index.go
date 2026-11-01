package main

import (
	"path"
	"sort"
	"strings"
)

// fileIndex is the head tree's files, sorted out once.
type fileIndex struct {
	cls     *Classifier
	files   map[string]Class
	byBase  map[string][]string
	tests   []string            // every test file, sorted
	byFam   map[string][]string // family → its test files
	sources map[string][]string // family → its source and UI-resource files
}

func newFileIndex(files []string) *fileIndex {
	idx := &fileIndex{
		cls:     newClassifier(files),
		files:   make(map[string]Class, len(files)),
		byBase:  map[string][]string{},
		byFam:   map[string][]string{},
		sources: map[string][]string{},
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, f := range sorted {
		c := idx.cls.classify(f)
		idx.files[f] = c
		idx.byBase[path.Base(f)] = append(idx.byBase[path.Base(f)], f)
		switch c.Kind {
		case KindTest:
			idx.tests = append(idx.tests, f)
			idx.byFam[c.Family] = append(idx.byFam[c.Family], f)
		case KindSource:
			idx.sources[c.Family] = append(idx.sources[c.Family], f)
		case KindOther:
			if c.Family != "" && isUIResource(f) {
				idx.sources[c.Family] = append(idx.sources[c.Family], f)
			}
		}
	}
	return idx
}

func (idx *fileIndex) has(p string) bool { _, ok := idx.files[p]; return ok }

func (idx *fileIndex) class(p string) Class {
	if c, ok := idx.files[p]; ok {
		return c
	}
	return idx.cls.classify(p)
}

// testsUnder lists the test files under dir (with its trailing slash).
func (idx *fileIndex) testsUnder(dir string) []string {
	i := sort.SearchStrings(idx.tests, dir)
	var out []string
	for ; i < len(idx.tests) && strings.HasPrefix(idx.tests[i], dir); i++ {
		out = append(out, idx.tests[i])
	}
	return out
}

// familyTestFiles lists the tests a source of this family is searched against.
func (idx *fileIndex) familyTestFiles(family string) []string {
	var out []string
	for _, f := range familyTests[family] {
		out = append(out, idx.byFam[f]...)
	}
	return out
}

// isUIResource is a file of user-visible strings the UI search reads.
func isUIResource(p string) bool {
	b := path.Base(p)
	switch {
	case strings.HasSuffix(b, ".xcstrings"):
		return true
	case strings.HasSuffix(p, "/res/values/strings.xml") || strings.Contains(p, "/res/values/strings_") && strings.HasSuffix(b, ".xml"):
		return true
	case p == "web/src/i18n/en.json":
		return true
	}
	return false
}
