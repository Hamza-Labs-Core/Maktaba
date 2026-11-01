package main

import (
	"path"
	"regexp"
	"strings"
)

// The web's test map: vitest runs a test file with every module it imports, so a source
// file's tests are the test files that import it, directly or through other modules.
// This is the static import graph `vitest related` walks, read from the tree: no run.

var reImport = regexp.MustCompile(`(?m)(?:^|[^.\w])(?:import|export)\s+(?:type\s+)?(?:[^'"` + "`" + `;]*?\s+from\s+)?["']([^"']+)["']` +
	`|\bimport\(\s*["']([^"']+)["']\s*\)` +
	`|\bvi\.(?:mock|importActual)\(\s*["']([^"']+)["']`)

// webAliases are vitest.config.ts's resolve.alias.
var webAliases = []struct{ prefix, dir string }{
	{"@/", "web/src/"},
	{"@ds/", "web/design-system/"},
	{"@maktaba/tvplayer/", "web/packages/tvplayer/src/"},
	{"@maktaba/tvplayer", "web/packages/tvplayer/src/index"},
}

// webGraph is the reverse import graph of web/: module → modules that import it.
type webGraph struct {
	importers map[string][]string
}

func buildWebGraph(repo Repo, idx *fileIndex) *webGraph {
	g := &webGraph{importers: map[string][]string{}}
	for f := range idx.files {
		if !strings.HasPrefix(f, "web/") || strings.Contains(f, "/node_modules/") {
			continue
		}
		if ext := path.Ext(f); ext != ".ts" && ext != ".tsx" {
			continue
		}
		b, err := repo.Read(f)
		if err != nil {
			continue
		}
		for _, m := range reImport.FindAllStringSubmatch(string(b), -1) {
			spec := m[1] + m[2] + m[3]
			if dep := resolveWebImport(f, spec, idx); dep != "" && dep != f {
				g.importers[dep] = append(g.importers[dep], f)
			}
		}
	}
	return g
}

func resolveWebImport(from, spec string, idx *fileIndex) string {
	var base string
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
		base = path.Join(path.Dir(from), spec)
	default:
		for _, a := range webAliases {
			if spec == strings.TrimSuffix(a.prefix, "/") || strings.HasPrefix(spec, a.prefix) {
				if strings.HasSuffix(a.prefix, "/") {
					base = a.dir + strings.TrimPrefix(spec, a.prefix)
				} else {
					base = a.dir
				}
				break
			}
		}
	}
	if base == "" {
		return "" // a package
	}
	for _, ext := range []string{".js", ".jsx", ".mjs"} {
		base = strings.TrimSuffix(base, ext)
	}
	for _, cand := range []string{base, base + ".ts", base + ".tsx", base + "/index.ts", base + "/index.tsx"} {
		if idx.has(cand) && (strings.HasSuffix(cand, ".ts") || strings.HasSuffix(cand, ".tsx")) {
			return cand
		}
	}
	return ""
}

// testsOf walks the importers of src up to the test files.
func (g *webGraph) testsOf(src string, idx *fileIndex) []string {
	seen := map[string]bool{src: true}
	queue := []string{src}
	var tests []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, imp := range g.importers[cur] {
			if seen[imp] {
				continue
			}
			seen[imp] = true
			if idx.class(imp).Kind == KindTest {
				tests = append(tests, imp)
				continue // a test imports; nothing imports a test
			}
			queue = append(queue, imp)
		}
	}
	return tests
}
