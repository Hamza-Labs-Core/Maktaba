package main

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The UI search: a changed UI file's accessibility identifiers and user-visible strings,
// looked up in the tests. A test that names "With a code" breaks when that label goes.

// TermMode is how a term is matched in a test.
type TermMode int

const (
	MatchQuoted TermMode = iota // "term", 'term' or `term`
	MatchWord                   // term with no identifier character either side (R.string.x)
	MatchPrefix                 // a quote then term (an identifier built with interpolation)
)

// Term is one identifier or string to look for.
type Term struct {
	Text string
	Mode TermMode
	ID   bool // an accessibility identifier / testTag / data-testid / string key
}

// strict terms name one control: an identifier, or words with a space ("With a code"),
// not one common word ("Home"). A test using one is about the changed control.
func (t Term) strict() bool {
	return t.ID && t.Mode != MatchPrefix || strings.ContainsAny(t.Text, "  ")
}

// tables are the string tables code refers to by key.
type tables struct {
	en    map[string]string   // web: i18n key → English
	swift map[string][]string // Apple: `.byCode: "With a code"` (every language's)
}

func (t Term) label() string {
	if t.Mode == MatchPrefix {
		return `"` + t.Text + `…"`
	}
	if t.Mode == MatchWord {
		return t.Text
	}
	return `"` + t.Text + `"`
}

var (
	reSwiftID   = regexp.MustCompile(`\.accessibilityIdentifier\(\s*"((?:[^"\\]|\\.)*)"`)
	reKotlinTag = regexp.MustCompile(`testTag\(\s*"((?:[^"\\]|\\.)*)"`)
	reWebTestID = regexp.MustCompile(`data-testid\s*=\s*\{?\s*["'` + "`" + `]([^"'` + "`" + `]+)["'` + "`" + `]`)
	reDQ        = regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"`)
	reSQ        = regexp.MustCompile(`'((?:[^'\\\n]|\\.)*)'`)
	reJSXText   = regexp.MustCompile(`>\s*([^<>{}\n]*\pL[^<>{}\n]*?)\s*<`)
	reTKey      = regexp.MustCompile(`\bt\(\s*["']([A-Za-z0-9_.\-]+)["']`)
	reXCKey     = regexp.MustCompile(`^    "((?:[^"\\]|\\.)+)" : \{\s*$`)
	reXCValue   = regexp.MustCompile(`^\s*"value"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	reAndroidS  = regexp.MustCompile(`<string\s+name="([^"]+)"[^>]*>(.*?)</string>`)
	reJSONPair  = regexp.MustCompile(`^\s*"([^"]+)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	reKeyLike   = regexp.MustCompile(`^[A-Za-z0-9_.\-/:%@#]+$`)
	reSwiftKey  = regexp.MustCompile(`\.text\(\s*\.([A-Za-z_][A-Za-z0-9_]*)`)
	reSwiftRow  = regexp.MustCompile(`(?m)^\s*\.([A-Za-z_][A-Za-z0-9_]*)\s*:\s*"((?:[^"\\\n]|\\.)*)",?\s*$`)
)

// uiSearchable is a changed file the UI search reads.
func uiSearchable(p string, c Class) bool {
	switch c.Family {
	case familyApple, familyAndroid, familyAndroidTV, familyDesktopPl, familyWeb:
	default:
		return false
	}
	if isUIResource(p) {
		return true
	}
	if c.Kind != KindSource {
		return false
	}
	switch path.Ext(p) {
	case ".swift", ".kt", ".java", ".tsx", ".ts":
		return true
	}
	return false
}

// identifierTerms are the identifiers in a whole file.
func identifierTerms(p string, content string) []Term {
	var re *regexp.Regexp
	switch path.Ext(p) {
	case ".swift":
		re = reSwiftID
	case ".kt", ".java":
		re = reKotlinTag
	case ".tsx", ".ts":
		re = reWebTestID
	default:
		return nil
	}
	var out []Term
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		if t, ok := idTerm(m[1]); ok {
			out = append(out, t)
		}
	}
	return out
}

// idTerm makes an identifier a term; one built with interpolation is matched by its
// fixed beginning ("player.row.\(id)" → "player.row.").
func idTerm(s string) (Term, bool) {
	for _, mark := range []string{`\(`, "${", "$"} {
		if i := strings.Index(s, mark); i >= 0 {
			s = s[:i]
			if utf8.RuneCountInString(s) < 5 {
				return Term{}, false
			}
			return Term{Text: s, Mode: MatchPrefix, ID: true}, true
		}
	}
	if utf8.RuneCountInString(s) < 3 {
		return Term{}, false
	}
	return Term{Text: s, Mode: MatchQuoted, ID: true}, true
}

// lineTerms are the identifiers and strings on changed lines of a file.
func lineTerms(p string, lines []Line, tb tables) []Term {
	var out []Term
	ext := path.Ext(p)
	base := path.Base(p)
	for _, l := range lines {
		text := l.Text
		trim := strings.TrimSpace(text)
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "/*") ||
			strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "import ") {
			continue
		}
		switch {
		case ext == ".xcstrings":
			if m := reXCKey.FindStringSubmatch(text); m != nil {
				if t, ok := stringTerm(m[1]); ok {
					out = append(out, t)
				}
			}
			if m := reXCValue.FindStringSubmatch(text); m != nil {
				if t, ok := stringTerm(m[1]); ok {
					out = append(out, t)
				}
			}
		case ext == ".xml":
			for _, m := range reAndroidS.FindAllStringSubmatch(text, -1) {
				out = append(out, Term{Text: "R.string." + m[1], Mode: MatchWord, ID: true})
				if t, ok := stringTerm(strings.ReplaceAll(m[2], `\'`, "'")); ok {
					out = append(out, t)
				}
			}
		case base == "en.json":
			if m := reJSONPair.FindStringSubmatch(text); m != nil {
				out = append(out, Term{Text: m[1], Mode: MatchQuoted, ID: true})
				if t, ok := stringTerm(m[2]); ok {
					out = append(out, t)
				}
			}
		default:
			for _, re := range []*regexp.Regexp{reSwiftID, reKotlinTag, reWebTestID} {
				for _, m := range re.FindAllStringSubmatch(text, -1) {
					if t, ok := idTerm(m[1]); ok {
						out = append(out, t)
					}
				}
			}
			lits := reDQ.FindAllStringSubmatch(text, -1)
			if ext == ".swift" {
				// words.text(.byCode): the key's strings in the app's tables
				for _, m := range reSwiftKey.FindAllStringSubmatch(text, -1) {
					for _, v := range tb.swift[m[1]] {
						if t, ok := stringTerm(v); ok {
							out = append(out, t)
						}
					}
				}
			}
			if ext == ".ts" || ext == ".tsx" {
				lits = append(lits, reSQ.FindAllStringSubmatch(text, -1)...)
				lits = append(lits, reJSXText.FindAllStringSubmatch(text, -1)...)
				for _, m := range reTKey.FindAllStringSubmatch(text, -1) {
					out = append(out, Term{Text: m[1], Mode: MatchQuoted, ID: true})
					if v, ok := tb.en[m[1]]; ok {
						if t, ok := stringTerm(v); ok {
							out = append(out, t)
						}
					}
				}
			}
			for _, m := range lits {
				if t, ok := stringTerm(m[1]); ok {
					out = append(out, t)
				}
			}
		}
	}
	return dedupTerms(out)
}

// stringTerm keeps a literal that reads as words a person sees: not a key, path, symbol
// name or format code.
func stringTerm(s string) (Term, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, `\(`) || strings.Contains(s, "${") {
		return Term{}, false
	}
	n := utf8.RuneCountInString(s)
	letters, latin, upperFirst := 0, true, false
	for i, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if r > unicode.MaxLatin1 {
				latin = false
			}
		}
		if i == 0 {
			upperFirst = unicode.IsUpper(r)
		}
	}
	switch {
	case letters < 3:
		return Term{}, false
	case latin && n < 4:
		return Term{}, false
	case strings.HasPrefix(s, "http") || strings.HasPrefix(s, "/"):
		return Term{}, false
	}
	hasSpace := strings.ContainsAny(s, "  ")
	if !hasSpace && reKeyLike.MatchString(s) {
		// one token: a word only when capitalised and plain ("Home"), never a key
		// ("player.home"), a symbol ("play.fill") or a constant
		if !upperFirst || strings.ContainsAny(s, "._/:-%@#") || strings.ToUpper(s) == s {
			return Term{}, false
		}
		for _, r := range s[1:] {
			if unicode.IsUpper(r) {
				return Term{}, false // CamelCase: a type or an identifier
			}
		}
	}
	return Term{Text: s, Mode: MatchQuoted}, true
}

func dedupTerms(ts []Term) []Term {
	seen := map[Term]bool{}
	var out []Term
	for _, t := range ts {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// matchTerm reports whether content uses the term.
func matchTerm(content string, t Term) bool {
	s := content
	for {
		i := strings.Index(s, t.Text)
		if i < 0 {
			return false
		}
		before := byte(0)
		if i > 0 {
			before = s[i-1]
		}
		j := i + len(t.Text)
		after := byte(0)
		if j < len(s) {
			after = s[j]
		}
		switch t.Mode {
		case MatchQuoted:
			if (before == '"' || before == '\'' || before == '`') && after == before {
				return true
			}
		case MatchPrefix:
			if before == '"' || before == '\'' || before == '`' {
				return true
			}
		case MatchWord:
			if !isIdentByte(before) && !isIdentByte(after) {
				return true
			}
		}
		s = s[i+1:]
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// loadEnglish reads the web's English strings (key → text).
func loadEnglish(repo Repo) map[string]string {
	b, err := repo.Read("web/src/i18n/en.json")
	if err != nil {
		return nil
	}
	var flat map[string]any
	if json.Unmarshal(b, &flat) != nil {
		return nil
	}
	out := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case string:
			out[prefix] = x
		case map[string]any:
			for k, vv := range x {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, vv)
			}
		}
	}
	walk("", flat)
	return out
}

// loadSwiftTables reads the Apple apps' keyed string tables (`.byCode: "With a code",`).
func loadSwiftTables(repo Repo, idx *fileIndex) map[string][]string {
	out := map[string][]string{}
	for _, f := range idx.sources[familyApple] {
		if !strings.HasSuffix(f, ".swift") {
			continue
		}
		b, err := repo.Read(f)
		if err != nil || !bytes.Contains(b, []byte(`: "`)) {
			continue
		}
		for _, m := range reSwiftRow.FindAllSubmatch(b, -1) {
			k, v := string(m[1]), string(m[2])
			if !contains(out[k], v) {
				out[k] = append(out[k], v)
			}
		}
	}
	return out
}

// uiTestScope is the tests a changed UI file is searched against: the Apple apps' own
// UI tests for their screens, the TV app's for its, every Apple test for MaktabaKit.
func uiTestScope(p string, c Class, idx *fileIndex) []string {
	all := idx.familyTestFiles(c.Family)
	if c.Family != familyApple || strings.HasPrefix(p, "apps/apple/MaktabaKit/") {
		return all
	}
	tv := strings.HasPrefix(p, "apps/tv/tvos/")
	var out []string
	for _, f := range all {
		if tv && strings.HasPrefix(f, "apps/tv/tvos/") ||
			!tv && (strings.HasPrefix(f, "apps/apple/UITests/") || strings.HasPrefix(f, "apps/apple/PlayerMacTests/")) {
			out = append(out, f)
		}
	}
	return out
}

// UIResult is what the UI search found for one changed file.
type UIResult struct {
	Hits   map[string][]string // test file → the terms it uses
	Strict map[string]bool     // test files using a strict term from a changed line
	Stale  []Stale
}

// Stale is a test that still uses a string or identifier no source has any more: it is
// sure to fail.
type Stale struct {
	Test, Term, From string
}

// uiSearch looks a changed file's terms up in its family's tests. removedGone says which
// removed terms no source of the family has any more; tests using those are stale.
func uiSearch(ch Change, c Class, repo Repo, idx *fileIndex, tb tables) UIResult {
	res := UIResult{Hits: map[string][]string{}, Strict: map[string]bool{}}
	content := ""
	if ch.Status != 'D' {
		if b, err := repo.Read(ch.Path); err == nil {
			content = string(b)
		}
	}
	added := lineTerms(ch.Path, ch.Added, tb)
	current := dedupTerms(append(identifierTerms(ch.Path, content), added...))
	removed := lineTerms(ch.Path, ch.Removed, tb)
	onLines := map[Term]bool{}
	for _, t := range append(append([]Term(nil), added...), removed...) {
		onLines[t] = true
	}
	// a term still on an added line of this file was edited around, not removed
	still := map[string]bool{}
	for _, t := range current {
		still[t.Text] = true
	}
	var gone []Term
	for _, t := range removed {
		if !still[t.Text] && t.Mode != MatchPrefix && !strings.Contains(content, t.Text) {
			gone = append(gone, t)
		}
	}
	gone = goneEverywhere(gone, c.Family, repo, idx)

	terms := dedupTerms(append(current, removed...))
	if len(terms) == 0 {
		return res
	}
	for _, f := range uiTestScope(ch.Path, c, idx) {
		b, err := repo.Read(f)
		if err != nil {
			continue
		}
		tc := string(b)
		for _, t := range terms {
			if matchTerm(tc, t) {
				res.Hits[f] = append(res.Hits[f], t.label())
				if onLines[t] && t.strict() {
					res.Strict[f] = true
				}
			}
		}
		for _, t := range gone {
			if matchTerm(tc, t) {
				res.Stale = append(res.Stale, Stale{Test: f, Term: t.label(), From: ch.Path})
			}
		}
	}
	return res
}

// goneEverywhere keeps the removed terms that no source or string table of the family
// has at the head (moved elsewhere is not gone).
func goneEverywhere(terms []Term, family string, repo Repo, idx *fileIndex) []Term {
	if len(terms) == 0 {
		return nil
	}
	left := map[string]Term{}
	for _, t := range terms {
		left[t.Text] = t
	}
	var fams []string
	fams = append(fams, family)
	if family == familyAndroid || family == familyAndroidTV || family == familyDesktopPl {
		fams = []string{familyAndroid, familyAndroidTV, familyDesktopPl}
	}
	for _, fam := range fams {
		for _, f := range idx.sources[fam] {
			if len(left) == 0 {
				return nil
			}
			b, err := repo.Read(f)
			if err != nil {
				continue
			}
			s := string(b)
			for text := range left {
				needle := text
				if strings.HasPrefix(text, "R.string.") {
					needle = `name="` + strings.TrimPrefix(text, "R.string.") + `"`
				}
				if strings.Contains(s, needle) {
					delete(left, text)
				}
			}
		}
	}
	out := make([]Term, 0, len(left))
	for _, t := range left {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return out
}
