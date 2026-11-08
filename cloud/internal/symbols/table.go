// Package symbols turns the Apple apps' crash frames into function names, files and lines
// at Maktaba (Epic 53 stories 53.14 and 53.16). An Apple crash is not symbolicated on the
// device: each frame is a binary and an offset into its __TEXT segment, with the binary's
// UUID ("Maktaba Remote + 0x1a2b"). A release's dSYMs hold the names; a symbol table is
// what Maktaba keeps of one binary's dSYM (one architecture): its functions' address
// ranges and its line table, small enough for the cloud's Postgres. The table is made
// where the dSYM is (`maktaba-symbols extract`, the release's machine), uploaded with
// `maktaba-symbols upload`, and a forwarded report's crashes are read against it when the
// service admin opens the report.
package symbols

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// Version is the table's format.
const Version = 1

// MaxTable is the largest table kept, compressed; MaxRaw is the most a table may unpack to.
const (
	MaxTable = 64 << 20
	MaxRaw   = 512 << 20
)

// Table is one binary's symbols (one architecture of its dSYM), or an Android release's
// R8 mapping (Kind KindR8, r8.go).
type Table struct {
	V int `json:"v"`
	// Kind is "" for a dSYM's table, KindR8 for a mapping's
	Kind     string `json:"kind,omitempty"`
	DebugID  string `json:"debug_id"`  // the binary's UUID, NormalizeID's form (a mapping's: R8ID)
	Arch     string `json:"arch"`      // arm64, x86_64, …
	CodeFile string `json:"code_file"` // the binary's name ("Maktaba Remote")
	// TextAddr is the __TEXT segment's address in the dSYM: a frame's offset is from it
	TextAddr uint64 `json:"text_addr"`
	// Names are the functions' names; Funcs are [low, high, name index], sorted by low
	Names []string    `json:"names"`
	Funcs [][3]uint64 `json:"funcs"`
	// Files are the line table's files; Lines are [address, file index + 1, line] sorted by
	// address, where a file index of 0 ends a sequence (no line after it)
	Files []string    `json:"files"`
	Lines [][3]uint64 `json:"lines"`

	// Release is a mapping's app version (its DebugID is R8ID(CodeFile, Release)); Classes
	// its classes
	Release string    `json:"release,omitempty"`
	Classes []R8Class `json:"classes,omitempty"`
	r8      *r8Index
}

// NormalizeID is a UUID as tables are keyed: lower case, no dashes or braces.
func NormalizeID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.NewReplacer("-", "", "{", "", "}", "").Replace(id)
}

// validID is a normalized UUID: 32 hex digits.
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Symbol is one address's place in the code.
type Symbol struct {
	Function string `json:"function"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Lookup is the function (and file and line, when the line table has them) at offset into
// the binary's __TEXT segment, as a crash frame names it.
func (t *Table) Lookup(offset uint64) (Symbol, bool) {
	addr := t.TextAddr + offset
	var s Symbol
	i := sort.Search(len(t.Funcs), func(i int) bool { return t.Funcs[i][0] > addr }) - 1
	// the innermost range holding addr: ranges sorted by low may nest, so look back a few
	for j := i; j >= 0 && j > i-8; j-- {
		f := t.Funcs[j]
		if addr >= f[0] && addr < f[1] && int(f[2]) < len(t.Names) {
			s.Function = t.Names[f[2]]
			break
		}
	}
	if s.Function == "" {
		return s, false
	}
	k := sort.Search(len(t.Lines), func(k int) bool { return t.Lines[k][0] > addr }) - 1
	if k >= 0 {
		if l := t.Lines[k]; l[1] > 0 && int(l[1]) <= len(t.Files) {
			s.File, s.Line = t.Files[l[1]-1], int(l[2])
		}
	}
	return s, true
}

// sortRows sorts the table's rows (Lookup needs them sorted).
func (t *Table) sortRows() {
	sort.SliceStable(t.Funcs, func(i, j int) bool {
		if t.Funcs[i][0] != t.Funcs[j][0] {
			return t.Funcs[i][0] < t.Funcs[j][0]
		}
		// a wider range first, so the narrower (inner) one is found looking back
		return t.Funcs[i][1] > t.Funcs[j][1]
	})
	sort.SliceStable(t.Lines, func(i, j int) bool { return t.Lines[i][0] < t.Lines[j][0] })
}

// Encode is the table as it is uploaded and kept: gzip'd JSON.
func (t *Table) Encode() ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(t); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ErrInvalid is a table that is not one.
var ErrInvalid = errors.New("symbols: not a symbol table")

// Decode reads a table Encode made, checking it: it may come from outside.
func Decode(b []byte) (*Table, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	raw, err := io.ReadAll(io.LimitReader(zr, MaxRaw+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(raw) > MaxRaw {
		return nil, fmt.Errorf("%w: it unpacks to more than %d MB", ErrInvalid, MaxRaw>>20)
	}
	var t Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if t.V != Version {
		return nil, fmt.Errorf("%w: format %d, not %d", ErrInvalid, t.V, Version)
	}
	t.DebugID = NormalizeID(t.DebugID)
	if !validID(t.DebugID) {
		return nil, fmt.Errorf("%w: debug_id is not a UUID", ErrInvalid)
	}
	t.CodeFile = clip(path.Base(t.CodeFile), 200)
	t.Arch = clip(t.Arch, 32)
	switch t.Kind {
	case "":
		if len(t.Funcs) == 0 {
			return nil, fmt.Errorf("%w: no functions", ErrInvalid)
		}
		t.Classes, t.Release = nil, ""
		t.sortRows()
	case KindR8:
		// a mapping is found by its app and version: its key must be theirs
		if len(t.Classes) == 0 {
			return nil, fmt.Errorf("%w: no classes", ErrInvalid)
		}
		if t.Release == "" || t.DebugID != R8ID(t.CodeFile, t.Release) {
			return nil, fmt.Errorf("%w: a mapping's debug_id is not its app's and release's", ErrInvalid)
		}
		t.Funcs, t.Lines = nil, nil
		t.indexR8()
	default:
		return nil, fmt.Errorf("%w: kind %q", ErrInvalid, t.Kind)
	}
	return &t, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
