package symbols

import (
	"debug/dwarf"
	"debug/macho"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// lcUUID is the Mach-O load command that names a binary's UUID.
const lcUUID = 0x1b

// ExtractPath is the tables of a dSYM bundle (X.dSYM: every binary in its
// Contents/Resources/DWARF) or of one Mach-O file with its debug info.
func ExtractPath(p string) ([]*Table, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	files := []string{p}
	if st.IsDir() {
		files, err = filepath.Glob(filepath.Join(p, "Contents", "Resources", "DWARF", "*"))
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: no Contents/Resources/DWARF files", p)
		}
	}
	var out []*Table
	for _, f := range files {
		ts, err := extractFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, ts...)
	}
	return out, nil
}

func extractFile(p string) ([]*Table, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Extract(f, filepath.Base(p))
}

// Extract is the tables of one Mach-O file (thin, or fat with one table per architecture).
func Extract(r io.ReaderAt, codeFile string) ([]*Table, error) {
	if fat, err := macho.NewFatFile(r); err == nil {
		var out []*Table
		for _, a := range fat.Arches {
			t, err := extractOne(a.File, codeFile)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", a.Cpu, err)
			}
			out = append(out, t)
		}
		return out, nil
	}
	f, err := macho.NewFile(r)
	if err != nil {
		return nil, err
	}
	t, err := extractOne(f, codeFile)
	if err != nil {
		return nil, err
	}
	return []*Table{t}, nil
}

func archName(c macho.Cpu) string {
	switch c {
	case macho.CpuArm64:
		return "arm64"
	case macho.CpuAmd64:
		return "x86_64"
	case macho.CpuArm:
		return "arm"
	case macho.Cpu386:
		return "i386"
	}
	return strings.ToLower(c.String())
}

// uuidOf is the binary's LC_UUID.
func uuidOf(f *macho.File) (string, bool) {
	for _, l := range f.Loads {
		raw := l.Raw()
		if len(raw) >= 24 && f.ByteOrder.Uint32(raw[0:4]) == lcUUID {
			return hex.EncodeToString(raw[8:24]), true
		}
	}
	return "", false
}

func extractOne(f *macho.File, codeFile string) (*Table, error) {
	id, ok := uuidOf(f)
	if !ok {
		return nil, errors.New("no LC_UUID")
	}
	text := f.Segment("__TEXT")
	if text == nil {
		return nil, errors.New("no __TEXT segment")
	}
	t := &Table{V: Version, DebugID: id, Arch: archName(f.Cpu), CodeFile: codeFile, TextAddr: text.Addr}
	names := map[string]uint64{}
	name := func(s string) uint64 {
		if i, ok := names[s]; ok {
			return i
		}
		names[s] = uint64(len(t.Names))
		t.Names = append(t.Names, s)
		return names[s]
	}
	if d, err := f.DWARF(); err == nil {
		if err := fromDWARF(d, t, name, "."); err != nil {
			return nil, err
		}
	}
	fromSymtab(f, t, name)
	if len(t.Funcs) == 0 {
		return nil, errors.New("no functions in its DWARF or symbol table")
	}
	t.sortRows()
	return t, nil
}

// scopeTags are the entries whose names qualify the functions inside them.
var scopeTags = map[dwarf.Tag]bool{
	dwarf.TagModule: true, dwarf.TagNamespace: true, dwarf.TagStructType: true, dwarf.TagClassType: true,
	dwarf.TagEnumerationType: true, dwarf.TagUnionType: true, dwarf.TagInterfaceType: true,
}

// fromDWARF adds each subprogram's ranges (named with its enclosing types and modules joined
// by sep, as "Demo.Player.play" for Swift or "media::Player::play" for C++) and every compile
// unit's line table.
func fromDWARF(d *dwarf.Data, t *Table, name func(string) uint64, sep string) error {
	type def struct {
		ranges [][2]uint64
		own    string
		ref    dwarf.Offset
	}
	qualified := map[dwarf.Offset]string{}
	var defs []def
	var scope []string
	files := map[string]uint64{}
	r := d.Reader()
	for {
		e, err := r.Next()
		if err != nil {
			return fmt.Errorf("dwarf: %w", err)
		}
		if e == nil {
			break
		}
		if e.Tag == 0 { // the end of an entry's children
			if len(scope) > 0 {
				scope = scope[:len(scope)-1]
			}
			continue
		}
		own, _ := e.Val(dwarf.AttrName).(string)
		if e.Tag == dwarf.TagCompileUnit {
			addLines(d, e, t, files)
		}
		if e.Tag == dwarf.TagSubprogram {
			q := own
			if q == "" {
				q, _ = e.Val(dwarf.AttrLinkageName).(string)
			}
			if q != "" {
				if p := strings.Join(nonEmpty(scope), sep); p != "" && own != "" {
					q = p + sep + q
				}
				qualified[e.Offset] = q
			}
			if rs, err := d.Ranges(e); err == nil && len(rs) > 0 {
				x := def{own: qualified[e.Offset]}
				if ref, ok := e.Val(dwarf.AttrSpecification).(dwarf.Offset); ok {
					x.ref = ref
				} else if ref, ok := e.Val(dwarf.AttrAbstractOrigin).(dwarf.Offset); ok {
					x.ref = ref
				}
				for _, rg := range rs {
					if rg[1] > rg[0] {
						x.ranges = append(x.ranges, [2]uint64{rg[0], rg[1]})
					}
				}
				defs = append(defs, x)
			}
		}
		if e.Children {
			s := ""
			if scopeTags[e.Tag] {
				s = own
			}
			scope = append(scope, s)
		}
	}
	for _, x := range defs {
		n := x.own
		if x.ref != 0 && qualified[x.ref] != "" {
			n = qualified[x.ref]
		}
		if n == "" {
			continue
		}
		i := name(n)
		for _, rg := range x.ranges {
			t.Funcs = append(t.Funcs, [3]uint64{rg[0], rg[1], i})
		}
	}
	return nil
}

func nonEmpty(s []string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// addLines adds one compile unit's line table.
func addLines(d *dwarf.Data, cu *dwarf.Entry, t *Table, files map[string]uint64) {
	lr, err := d.LineReader(cu)
	if err != nil || lr == nil {
		return
	}
	var le dwarf.LineEntry
	for {
		if err := lr.Next(&le); err != nil {
			return
		}
		if le.EndSequence {
			t.Lines = append(t.Lines, [3]uint64{le.Address, 0, 0})
			continue
		}
		if le.File == nil || le.Line <= 0 {
			continue
		}
		fi, ok := files[le.File.Name]
		if !ok {
			t.Files = append(t.Files, le.File.Name)
			fi = uint64(len(t.Files))
			files[le.File.Name] = fi
		}
		t.Lines = append(t.Lines, [3]uint64{le.Address, fi, uint64(le.Line)})
	}
}

// fromSymtab adds the symbol table's functions the DWARF does not cover (a binary with no
// debug info, or code built without it), each up to the next symbol.
func fromSymtab(f *macho.File, t *Table, name func(string) uint64) {
	if f.Symtab == nil {
		return
	}
	text := f.Section("__text")
	if text == nil {
		return
	}
	end := text.Addr + text.Size
	var syms []macho.Symbol
	for _, s := range f.Symtab.Syms {
		// a defined symbol (N_SECT) in __text, not a debugging (stab) one
		if s.Type&0xe0 == 0 && s.Type&0x0e == 0x0e && s.Value >= text.Addr && s.Value < end && s.Name != "" {
			syms = append(syms, s)
		}
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })
	covered := append([][3]uint64(nil), t.Funcs...)
	sort.Slice(covered, func(i, j int) bool { return covered[i][0] < covered[j][0] })
	in := func(a uint64) bool {
		i := sort.Search(len(covered), func(i int) bool { return covered[i][0] > a }) - 1
		for j := i; j >= 0 && j > i-8; j-- {
			if a >= covered[j][0] && a < covered[j][1] {
				return true
			}
		}
		return false
	}
	for i, s := range syms {
		hi := end
		for k := i + 1; k < len(syms); k++ {
			if syms[k].Value > s.Value {
				hi = syms[k].Value
				break
			}
		}
		if hi <= s.Value || in(s.Value) {
			continue
		}
		// C symbols carry a leading underscore in Mach-O
		t.Funcs = append(t.Funcs, [3]uint64{s.Value, hi, name(strings.TrimPrefix(s.Name, "_"))})
	}
}
