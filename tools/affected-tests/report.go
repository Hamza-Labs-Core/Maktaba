package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

func viaList(h *Hit) string {
	var v []string
	for _, k := range []string{ViaMap, ViaNaming, ViaPackage, ViaImports, ViaUI, ViaInline} {
		if h.Via[k] {
			if k == ViaUI && len(h.Terms) > 0 {
				terms := h.Terms
				more := ""
				if len(terms) > 3 {
					more = fmt.Sprintf(" +%d", len(terms)-3)
					terms = terms[:3]
				}
				v = append(v, "ui: "+strings.Join(terms, ", ")+more)
				continue
			}
			v = append(v, k)
		}
	}
	return strings.Join(v, "; ")
}

// hitsByPlatform groups the affected tests under their platforms, in listing order.
func hitsByPlatform(hits map[string]*Hit) [][]*Hit {
	groups := map[string][]*Hit{}
	for _, h := range hits {
		groups[h.Platform] = append(groups[h.Platform], h)
	}
	var out [][]*Hit
	for _, p := range platformOrder {
		g := groups[p]
		if len(g) == 0 {
			continue
		}
		sort.Slice(g, func(i, j int) bool { return g[i].File < g[j].File })
		out = append(out, g)
	}
	return out
}

func printList(w io.Writer, a *Analysis, rangeLabel string) {
	src, tests := len(a.Sources), len(a.ChangedTests)
	fmt.Fprintf(w, "affected-tests: %s: %d files changed (%d source, %d test, %d other)\n",
		rangeLabel, len(a.Changes), src, tests, len(a.Changes)-src-tests)
	fmt.Fprintf(w, "coverage map: %s\n", a.MapNote)
	if len(a.Hits) == 0 {
		fmt.Fprintln(w, "\nNo affected tests found.")
		return
	}
	fmt.Fprintln(w, "\nAffected tests")
	for _, g := range hitsByPlatform(a.Hits) {
		fmt.Fprintf(w, "  %s (%d)\n", platformTitle[g[0].Platform], len(g))
		for _, h := range g {
			mark := " "
			if a.ChangedTests[h.File] {
				mark = "*"
			}
			fmt.Fprintf(w, "   %s %s  [%s]\n", mark, h.File, viaList(h))
		}
	}
	fmt.Fprintln(w, "  (* changed in this range)")
}

func printGate(w io.Writer, a *Analysis) {
	fmt.Fprintln(w, "\nGate: every changed source file needs one of its tests changed too")
	if len(a.Sources) == 0 {
		fmt.Fprintln(w, "  no app or service source changed")
	}
	for _, s := range a.Sources {
		switch {
		case len(s.MissingPlatforms) > 0:
			var names []string
			for _, p := range s.MissingPlatforms {
				names = append(names, platformTitle[p])
			}
			fmt.Fprintf(w, "  MISSING  %s: %s use what its changed lines show, and none of them changed (%s)\n",
				s.Path, strings.Join(names, ", "), shortList(testsOfPlatforms(s, a, s.MissingPlatforms), 3))
		case s.Satisfied && s.InlineChanged && len(s.Changed) == 0:
			fmt.Fprintf(w, "  ok       %s (its inline tests changed)\n", s.Path)
		case s.Satisfied && s.Uncovered:
			fmt.Fprintf(w, "  ok       %s (no test found for it; a test of its project changed)\n", s.Path)
		case s.Satisfied:
			fmt.Fprintf(w, "  ok       %s (%s)\n", s.Path, shortList(s.Changed, 2))
		case s.Uncovered:
			fmt.Fprintf(w, "  MISSING  %s: no test covers it, and no test of its project changed; add one\n", s.Path)
		case s.Inline && len(s.Tests) == 0:
			fmt.Fprintf(w, "  MISSING  %s: its #[cfg(test)] tests did not change\n", s.Path)
		default:
			fmt.Fprintf(w, "  MISSING  %s: none of its %d tests changed (%s)\n", s.Path, len(s.Tests), shortList(s.Tests, 3))
		}
	}
	if len(a.Stale) > 0 {
		fmt.Fprintln(w, "\nStale tests: they use a string or identifier that no source has any more")
		for _, s := range a.Stale {
			fmt.Fprintf(w, "  STALE    %s uses %s (removed in %s)\n", s.Test, s.Term, s.From)
		}
	}
	for _, n := range a.Notes {
		fmt.Fprintf(w, "\nnote: %s\n", n)
	}
	switch {
	case a.Override != "" && (len(a.Stale) > 0 || anyUnsatisfied(a)):
		fmt.Fprintf(w, "\nResult: PASS by trailer. No-Test-Change: %s\n", a.Override)
	case a.Override != "":
		fmt.Fprintf(w, "\nResult: PASS (No-Test-Change trailer present: %s)\n", a.Override)
	case a.Blocked():
		fmt.Fprintln(w, "\nResult: BLOCKED. Update the tests above, or, when no test change is right,")
		fmt.Fprintln(w, "add a trailer to a commit in the range:  No-Test-Change: <why>")
	default:
		fmt.Fprintln(w, "\nResult: PASS")
	}
}

// testsOfPlatforms lists a source's UI-search tests on the given platforms.
func testsOfPlatforms(s *SourceResult, a *Analysis, platforms []string) []string {
	var out []string
	for _, f := range s.Tests {
		h := a.Hits[f]
		if h != nil && h.Via[ViaUI] && contains(platforms, h.Platform) {
			out = append(out, f)
		}
	}
	return out
}

func anyUnsatisfied(a *Analysis) bool {
	for _, s := range a.Sources {
		if !s.Satisfied {
			return true
		}
	}
	return false
}

func shortList(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + fmt.Sprintf(" +%d more", len(xs)-n)
}

// jsonReport is `list --json`.
type jsonReport struct {
	Range    string               `json:"range"`
	Map      string               `json:"map"`
	Blocked  bool                 `json:"blocked"`
	Override string               `json:"override,omitempty"`
	Tests    map[string][]jsonHit `json:"tests"`
	Sources  []*SourceResult      `json:"sources"`
	Stale    []Stale              `json:"stale,omitempty"`
}

type jsonHit struct {
	File      string   `json:"file"`
	Via       []string `json:"via"`
	Selectors []string `json:"selectors,omitempty"`
	Terms     []string `json:"terms,omitempty"`
	Changed   bool     `json:"changed"`
}

func printJSON(w io.Writer, a *Analysis, rangeLabel string) error {
	r := jsonReport{Range: rangeLabel, Map: a.MapNote, Blocked: a.Blocked(), Override: a.Override,
		Tests: map[string][]jsonHit{}, Sources: a.Sources, Stale: a.Stale}
	for _, g := range hitsByPlatform(a.Hits) {
		for _, h := range g {
			jh := jsonHit{File: h.File, Terms: h.Terms, Changed: a.ChangedTests[h.File]}
			for v := range h.Via {
				jh.Via = append(jh.Via, v)
			}
			for s := range h.Selectors {
				jh.Selectors = append(jh.Selectors, s)
			}
			sort.Strings(jh.Via)
			sort.Strings(jh.Selectors)
			r.Tests[h.Platform] = append(r.Tests[h.Platform], jh)
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
