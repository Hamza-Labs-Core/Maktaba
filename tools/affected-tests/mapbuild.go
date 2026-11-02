package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Granularity of each measurement, as the map records it.
const (
	granGo      = "package: each Go package's unit tests (-short) with -coverpkg over its whole module"
	granPython  = "test: each pytest test (pytest-cov --cov-context=test)"
	granXccov   = "target: an Apple test target's run (xccov), all its test files"
	granJacoco  = "module: a Gradle module's JVM unit tests (JaCoCo), its own classes"
	sectionGo   = "go"
	sectionPy   = "python"
	idFileExt   = ".id"
	xccovSuffix = ".xccov.json"
	jacocoSufx  = ".jacoco.xml"
)

// buildGoSection reads CI's Go coverage: dir/index.tsv lists "<package dir>\t<profile>",
// one coverprofile per test package, measured with -coverpkg=<module>/....
func buildGoSection(dir string, idx *fileIndex, modules map[string]string) (*MapSection, error) {
	f, err := os.Open(filepath.Join(dir, "index.tsv"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := newSectionBuilder(granGo)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		pkgDir, prof := parts[0], parts[1]
		if !filepath.IsAbs(prof) {
			prof = filepath.Join(dir, prof)
		}
		pf, err := os.Open(prof)
		if err != nil {
			continue // a package whose run failed has no profile: its tests stay unmapped
		}
		err = parseCoverProfile(pf, modules, func(src string) {
			if idx.class(src).Kind == KindSource {
				b.add(src, PGo+":"+pkgDir)
			}
		})
		pf.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", prof, err)
		}
	}
	return b.done(), sc.Err()
}

// parseCoverProfile calls covered for each file with a block run at least once.
func parseCoverProfile(r io.Reader, modules map[string]string, covered func(src string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	seen := map[string]bool{}
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if strings.HasPrefix(line, "mode:") {
				continue
			}
		}
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			return fmt.Errorf("bad profile line %q", line)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("bad profile line %q", line)
		}
		if count == 0 {
			continue
		}
		imp := line[:colon]
		if seen[imp] {
			continue
		}
		seen[imp] = true
		if src := importToRepoPath(imp, modules); src != "" {
			covered(src)
		}
	}
	return sc.Err()
}

// importToRepoPath maps "github.com/x/repo/api/internal/a/b.go" to "api/internal/a/b.go"
// with the repo's modules (module path → dir).
func importToRepoPath(imp string, modules map[string]string) string {
	best := ""
	for mp := range modules {
		if strings.HasPrefix(imp, mp+"/") && len(mp) > len(best) {
			best = mp
		}
	}
	if best == "" {
		return ""
	}
	rest := strings.TrimPrefix(imp, best+"/")
	if d := modules[best]; d != "." {
		return d + "/" + rest
	}
	return rest
}

// goModulePaths reads each go.mod's module path: module path → its dir.
func goModulePaths(root string, files []string) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		if path.Base(f) != "go.mod" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "module ") {
				out[strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, "module ")), `"`)] = path.Dir(f)
				break
			}
		}
	}
	return out
}

// coverage.py's JSON report with --show-contexts.
type pyCoverage struct {
	Files map[string]struct {
		Contexts map[string][]string `json:"contexts"`
	} `json:"files"`
}

// pyCallLines are the lines of a Python file that run when a function is called, not when
// the module is imported: import-time lines run once, under whichever test imported the
// module first, and would pin every module on that one test.
func pyCallLines(src []byte) map[int]bool {
	type opener struct {
		indent int
		def    bool
	}
	var stack []opener
	out := map[int]bool{}
	for i, line := range strings.Split(string(src), "\n") {
		trim := strings.TrimLeft(line, " \t")
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := len(line) - len(trim)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		inDef := false
		for _, o := range stack {
			if o.def {
				inDef = true
			}
		}
		if inDef {
			out[i+1] = true
		}
		switch {
		case strings.HasPrefix(trim, "def "), strings.HasPrefix(trim, "async def "):
			stack = append(stack, opener{indent, true})
		case strings.HasPrefix(trim, "class "):
			stack = append(stack, opener{indent, false})
		}
	}
	return out
}

// buildPythonSection reads `coverage json --show-contexts` from a --cov-context=test run.
// Paths in it are relative to pyRoot (pipeline/); the sources are read under root to keep
// only the lines that run inside functions.
func buildPythonSection(file, root, pyRoot string, idx *fileIndex) (*MapSection, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var cov pyCoverage
	if err := json.Unmarshal(raw, &cov); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	b := newSectionBuilder(granPython)
	for _, src := range sortedKeys(cov.Files) {
		repoPath := path.Join(pyRoot, filepath.ToSlash(src))
		if idx.class(repoPath).Kind != KindSource {
			continue
		}
		var calls map[int]bool
		if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(repoPath))); err == nil {
			calls = pyCallLines(b)
		}
		var ids []string
		for line, ctxs := range cov.Files[src].Contexts {
			if n, err := strconv.Atoi(line); calls != nil && (err != nil || !calls[n]) {
				continue
			}
			for _, ctx := range ctxs {
				if id := pythonTestID(ctx, pyRoot); id != "" {
					ids = append(ids, id)
				}
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			b.add(repoPath, id)
		}
	}
	return b.done(), nil
}

// pythonTestID: "tests/a/test_b.py::TestC::test_d[x]|run" → "python:pipeline/tests/a/test_b.py::TestC::test_d".
func pythonTestID(ctx, pyRoot string) string {
	if i := strings.LastIndex(ctx, "|"); i >= 0 {
		ctx = ctx[:i]
	}
	if i := strings.Index(ctx, "["); i >= 0 {
		ctx = ctx[:i]
	}
	if ctx == "" || !strings.Contains(ctx, "::") {
		return ""
	}
	return PPython + ":" + path.Join(pyRoot, ctx[:strings.Index(ctx, "::")]) + ctx[strings.Index(ctx, "::"):]
}

// xccov's `xcrun xccov view --report --json` output.
type xccovReport struct {
	Targets []struct {
		Name  string `json:"name"`
		Files []struct {
			Path         string `json:"path"`
			CoveredLines int    `json:"coveredLines"`
		} `json:"files"`
	} `json:"targets"`
}

// addXccov adds an xccov report: every source file it covers maps to testID.
func addXccov(b *sectionBuilder, file, testID string, idx *fileIndex) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var rep xccovReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		// some Xcode versions print a bare list of targets
		var targets []json.RawMessage
		if json.Unmarshal(raw, &targets) != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		wrapped, _ := json.Marshal(map[string]any{"targets": targets})
		if err := json.Unmarshal(wrapped, &rep); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
	for _, t := range rep.Targets {
		for _, f := range t.Files {
			if f.CoveredLines == 0 {
				continue
			}
			if src := repoPathOf(f.Path, idx); src != "" && idx.class(src).Kind == KindSource {
				b.add(src, testID)
			}
		}
	}
	return nil
}

// repoPathOf finds the tracked file an absolute path of a CI runner names.
func repoPathOf(abs string, idx *fileIndex) string {
	abs = filepath.ToSlash(abs)
	for i := 0; i < len(abs); i++ {
		if abs[i] == '/' && idx.has(abs[i+1:]) {
			return abs[i+1:]
		}
	}
	if idx.has(abs) {
		return abs
	}
	return ""
}

// addJacoco adds a JaCoCo XML report: each source file with a covered line maps to testID.
func addJacoco(b *sectionBuilder, file, testID string, idx *fileIndex) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	// "com/x/Foo.kt" → the tracked files ending in it
	bySuffix := map[string][]string{}
	for p, c := range idx.files {
		if c.Kind == KindSource && (strings.HasSuffix(p, ".kt") || strings.HasSuffix(p, ".java")) {
			for _, root := range []string{"/kotlin/", "/java/"} {
				if i := strings.Index(p, root); i >= 0 {
					bySuffix[p[i+len(root):]] = append(bySuffix[p[i+len(root):]], p)
				}
			}
		}
	}
	module := ""
	if t, ok := parseTestID(testID); ok {
		module = gradleModule(t.Path + "/")
	}
	dec := xml.NewDecoder(f)
	dec.Strict = false
	var pkg, src string
	srcCovered := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "package":
				pkg = attr(e, "name")
			case "sourcefile":
				src, srcCovered = attr(e, "name"), false
			case "counter":
				if src != "" && attr(e, "type") == "LINE" && attr(e, "covered") != "0" && attr(e, "covered") != "" {
					srcCovered = true
				}
			}
		case xml.EndElement:
			if e.Name.Local == "sourcefile" {
				if srcCovered {
					cands := bySuffix[path.Join(pkg, src)]
					var mine []string
					for _, c := range cands {
						if module != "" && strings.HasPrefix(c, module+"/") {
							mine = append(mine, c)
						}
					}
					if len(mine) == 0 {
						mine = cands
					}
					for _, c := range mine {
						b.add(c, testID)
					}
				}
				src = ""
			}
		}
	}
	return nil
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// MapOptions is the `map` subcommand's input.
type MapOptions struct {
	Root      string
	Previous  string            // an earlier map whose other sections are kept
	GoDir     string            // CI's Go coverage directory (index.tsv)
	PyJSON    string            // coverage.py JSON with contexts
	PyRoot    string            // what the Python paths are relative to
	Xccov     map[string]string // xccov report → test ID
	Jacoco    map[string]string // JaCoCo XML → test ID
	Fragments string            // a directory of *.xccov.json / *.jacoco.xml, each with a .id file
	Commit    string
	Generated string
}

func buildMap(o MapOptions, files []string) (*CoverageMap, []string, error) {
	idx := newFileIndex(files)
	next := newMap()
	next.Commit, next.Generated = o.Commit, o.Generated
	var notes []string
	stamp := func(s *MapSection) *MapSection {
		s.Commit, s.Generated = o.Commit, o.Generated
		return s
	}
	if o.GoDir != "" {
		s, err := buildGoSection(o.GoDir, idx, goModulePaths(o.Root, files))
		if err != nil {
			notes = append(notes, "go: "+err.Error())
		} else {
			next.Sections[sectionGo] = stamp(s)
		}
	}
	if o.PyJSON != "" {
		s, err := buildPythonSection(o.PyJSON, o.Root, o.PyRoot, idx)
		if err != nil {
			notes = append(notes, "python: "+err.Error())
		} else {
			next.Sections[sectionPy] = stamp(s)
		}
	}
	xc, jc := map[string]string{}, map[string]string{}
	for k, v := range o.Xccov {
		xc[k] = v
	}
	for k, v := range o.Jacoco {
		jc[k] = v
	}
	if o.Fragments != "" {
		_ = filepath.Walk(o.Fragments, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			id, rerr := os.ReadFile(p + idFileExt)
			if rerr != nil {
				return nil
			}
			switch {
			case strings.HasSuffix(p, xccovSuffix):
				xc[p] = strings.TrimSpace(string(id))
			case strings.HasSuffix(p, jacocoSufx):
				jc[p] = strings.TrimSpace(string(id))
			}
			return nil
		})
	}
	// one section per report, named for its file: the iPhone and iPad runs may share a
	// test ID, and a report missing from this run keeps its previous section
	for _, file := range sortedKeys(xc) {
		id := xc[file]
		b := newSectionBuilder(granXccov)
		if err := addXccov(b, file, id, idx); err != nil {
			notes = append(notes, "xccov "+file+": "+err.Error())
			continue
		}
		next.Sections["xccov:"+reportName(file)] = stamp(b.done())
	}
	for _, file := range sortedKeys(jc) {
		id := jc[file]
		b := newSectionBuilder(granJacoco)
		if err := addJacoco(b, file, id, idx); err != nil {
			notes = append(notes, "jacoco "+file+": "+err.Error())
			continue
		}
		next.Sections["jacoco:"+reportName(file)] = stamp(b.done())
	}
	out := newMap()
	if o.Previous != "" {
		if prev, err := loadMap(o.Previous); err == nil {
			out = prev
		} else {
			notes = append(notes, "previous map not used: "+err.Error())
		}
	}
	out.merge(next)
	// drop files no longer in the tree
	for _, s := range out.Sections {
		for f := range s.Files {
			if !idx.has(f) {
				delete(s.Files, f)
			}
		}
	}
	return out, notes, nil
}

// reportName: build/coverage-fragment/player-mac.xccov.json → "player-mac".
func reportName(file string) string {
	b := filepath.Base(file)
	for _, s := range []string{xccovSuffix, jacocoSufx, ".json", ".xml"} {
		b = strings.TrimSuffix(b, s)
	}
	return b
}

// pairFlag reads repeated "file=id" flags.
type pairFlag map[string]string

func (p pairFlag) String() string {
	var b bytes.Buffer
	for k, v := range p {
		fmt.Fprintf(&b, "%s=%s ", k, v)
	}
	return b.String()
}

func (p pairFlag) Set(s string) error {
	i := strings.Index(s, "=")
	if i <= 0 || i == len(s)-1 {
		return fmt.Errorf("want file=test-id, got %q", s)
	}
	p[s[:i]] = s[i+1:]
	return nil
}
