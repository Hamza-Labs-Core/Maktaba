package symbols

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The Android apps' release builds are shrunk and obfuscated by R8: a crash's Kotlin frames
// name classes and methods as "a.b.c" with the obfuscated build's lines. The release's
// mapping (mapping.txt, ProGuard's format) names what each was; a table of Kind KindR8 is what
// Maktaba keeps of one: its classes and their methods' line ranges, without fields.
//
// Unlike a binary, an Android release has no UUID its crashes carry. A mapping table is keyed
// by its app and version instead (R8ID), the two a forwarded report names ("android.player",
// "0.3.102"): each release is one mapping per app.

// KindR8 is a table made of an R8 (or ProGuard) mapping; "" is one made of a dSYM.
const KindR8 = "r8"

// R8Class is one class of a mapping: its obfuscated and original names, its source file
// when the mapping says, and its methods in the mapping's order.
type R8Class struct {
	Obf     string     `json:"o"`
	Name    string     `json:"n"`
	File    string     `json:"f,omitempty"`
	Methods []R8Method `json:"m,omitempty"`
}

// R8Method is one method line of a mapping. A and B are the obfuscated build's lines it
// covers (0, 0: no range); S and E the original lines (S 0: the same as the obfuscated ones;
// E 0: every obfuscated line maps to S, as an inlined caller's do). Name is the original
// name, qualified with its class when it was inlined from another one. Several lines with
// one range are one frame's inline chain, the innermost first.
type R8Method struct {
	Obf  string `json:"o"`
	Name string `json:"n"`
	A    int    `json:"a,omitempty"`
	B    int    `json:"b,omitempty"`
	S    int    `json:"s,omitempty"`
	E    int    `json:"e,omitempty"`
}

// r8Index is a mapping table's lookups, built once when it is made or decoded.
type r8Index struct {
	byObf  map[string]*R8Class
	fileOf map[string]string // original class name → source file
}

// R8ID is the key of an app's release mapping: a UUID's form of the app and its version (a
// leading "v" dropped, so "v0.3.102" and "0.3.102" are one release).
func R8ID(app, version string) string {
	sum := sha256.Sum256([]byte("maktaba-r8\x00" + strings.TrimSpace(app) + "\x00" +
		strings.TrimPrefix(strings.TrimSpace(version), "v")))
	return hex.EncodeToString(sum[:16])
}

// ParseR8 reads an R8 or ProGuard mapping into a table for app's release version.
func ParseR8(r io.Reader, app, version string) (*Table, error) {
	app, version = strings.TrimSpace(app), strings.TrimPrefix(strings.TrimSpace(version), "v")
	if app == "" || version == "" {
		return nil, fmt.Errorf("a mapping needs its app and version")
	}
	t := &Table{V: Version, Kind: KindR8, DebugID: R8ID(app, version), CodeFile: app, Arch: "jvm", Release: version}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var cur *R8Class
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
		case strings.HasPrefix(trimmed, "#"):
			// a class's source file: # {"id":"sourceFile","fileName":"Player.kt"}
			if cur != nil && strings.Contains(trimmed, `"sourceFile"`) {
				var meta struct {
					ID       string `json:"id"`
					FileName string `json:"fileName"`
				}
				if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))), &meta) == nil &&
					meta.ID == "sourceFile" {
					cur.File = clip(meta.FileName, 200)
				}
			}
		case line[0] != ' ' && line[0] != '\t':
			orig, obf, ok := strings.Cut(strings.TrimSuffix(trimmed, ":"), " -> ")
			if !ok || !strings.HasSuffix(trimmed, ":") {
				return nil, fmt.Errorf("%w: line %d is not a class or member", ErrInvalid, n)
			}
			t.Classes = append(t.Classes, R8Class{Obf: clip(obf, 500), Name: clip(orig, 500)})
			cur = &t.Classes[len(t.Classes)-1]
		default:
			if cur == nil {
				return nil, fmt.Errorf("%w: line %d is a member before any class", ErrInvalid, n)
			}
			if m, ok := parseR8Member(trimmed); ok {
				cur.Methods = append(cur.Methods, m)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(t.Classes) == 0 {
		return nil, fmt.Errorf("%w: no classes in the mapping", ErrInvalid)
	}
	t.indexR8()
	return t, nil
}

// parseR8Member reads "[a:b:]type name(args)[:s[:e]] -> obf"; a field (no parenthesis) is
// not kept.
func parseR8Member(s string) (R8Method, bool) {
	left, obf, ok := strings.Cut(s, " -> ")
	if !ok {
		return R8Method{}, false
	}
	open, closing := strings.IndexByte(left, '('), strings.LastIndexByte(left, ')')
	if open < 0 || closing < open {
		return R8Method{}, false
	}
	m := R8Method{Obf: clip(strings.TrimSpace(obf), 500)}
	head := left[:open]
	// the obfuscated range: two numbers and a colon each, before the return type
	if a, rest, ok := leadingNumber(head); ok {
		if b, rest2, ok := leadingNumber(rest); ok {
			m.A, m.B, head = a, b, rest2
		}
	}
	if sp := strings.LastIndexByte(head, ' '); sp >= 0 {
		m.Name = clip(head[sp+1:], 500)
	} else {
		m.Name = clip(head, 500)
	}
	if m.Name == "" {
		return R8Method{}, false
	}
	// the original range after the arguments: ":s" or ":s:e"
	if tail := left[closing+1:]; strings.HasPrefix(tail, ":") {
		parts := strings.Split(tail[1:], ":")
		if v, err := strconv.Atoi(parts[0]); err == nil {
			m.S = v
		}
		if len(parts) > 1 {
			if v, err := strconv.Atoi(parts[1]); err == nil {
				m.E = v
			}
		}
	}
	return m, true
}

// leadingNumber reads "123:" off the front of s.
func leadingNumber(s string) (int, string, bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return 0, s, false
	}
	v, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, s, false
	}
	return v, s[i+1:], true
}

func (t *Table) indexR8() {
	idx := &r8Index{byObf: make(map[string]*R8Class, len(t.Classes)), fileOf: map[string]string{}}
	for i := range t.Classes {
		c := &t.Classes[i]
		idx.byObf[c.Obf] = c
		if c.File != "" {
			idx.fileOf[c.Name] = c.File
		}
	}
	t.r8 = idx
}

// RetraceClass is an obfuscated class's original name.
func (t *Table) RetraceClass(obf string) (string, bool) {
	if t.r8 == nil {
		t.indexR8()
	}
	if c := t.r8.byObf[obf]; c != nil {
		return c.Name, true
	}
	return "", false
}

// Retrace is an obfuscated frame's original frames, the innermost first (more than one when
// R8 inlined calls into it): class.method at line of the obfuscated build (0: unknown). A
// class the mapping does not rename is none of its business (nil).
func (t *Table) Retrace(class, method string, line int) []Symbol {
	if t.r8 == nil {
		t.indexR8()
	}
	c := t.r8.byObf[class]
	if c == nil {
		return nil
	}
	var named []R8Method
	for _, m := range c.Methods {
		if m.Obf == method {
			named = append(named, m)
		}
	}
	if len(named) == 0 {
		// the class was renamed, the method was not (its line is the obfuscated build's)
		return []Symbol{{Function: c.Name + "." + method, File: c.File}}
	}
	var out []Symbol
	if line > 0 {
		for _, m := range named {
			if m.A > 0 && line >= m.A && line <= m.B {
				out = append(out, t.r8Symbol(c, m, line))
			}
		}
		if len(out) == 0 {
			// R8 writes a method with one line and no range when it has only that one
			for _, m := range named {
				if m.A == 0 {
					out = append(out, t.r8Symbol(c, m, line))
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	// no line, or none that fits: the method's original names, its line unknown. A call
	// inlined into it (a line followed by another with its range) is not the method itself.
	var names []string
	var first R8Method
	for i, m := range named {
		if m.A > 0 && i+1 < len(named) && named[i+1].A == m.A && named[i+1].B == m.B {
			continue
		}
		if n := qualify(c, m.Name); !containsStr(names, n) {
			if len(names) == 0 {
				first = m
			}
			names = append(names, n)
		}
	}
	s := Symbol{Function: strings.Join(names, " | ")}
	if len(names) == 1 {
		s.File = t.fileOf(c, first.Name)
	}
	return []Symbol{s}
}

func (t *Table) r8Symbol(c *R8Class, m R8Method, line int) Symbol {
	orig := line
	switch {
	case m.S > 0 && m.E > 0 && m.E-m.S == m.B-m.A:
		orig = m.S + (line - m.A)
	case m.S > 0:
		orig = m.S
	}
	return Symbol{Function: qualify(c, m.Name), File: t.fileOf(c, m.Name), Line: orig}
}

// qualify is a method's name with its class: its own, or the one R8 inlined it from.
func qualify(c *R8Class, name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return c.Name + "." + name
}

// fileOf is the source file of a method's class, when the mapping names it.
func (t *Table) fileOf(c *R8Class, name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return t.r8.fileOf[name[:i]]
	}
	return c.File
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
