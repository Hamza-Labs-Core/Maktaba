package symbols

import (
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The Android apps' native crashes (Epic 53 story 53.14, Tombstone.kt) name each frame's
// library by its file name and GNU build id, and its pc relative to the library: an address in
// the library's own (ELF) address space, as addr2line reads it. The release's shipped libraries
// are stripped; a table of Kind KindELF is what Maktaba keeps of one unstripped library: its
// functions' address ranges (DWARF, else its symbol table) and its line table, keyed by its
// build id.

// KindELF is a table made of an unstripped ELF shared library (an Android app's own .so).
const KindELF = "elf"

// validCodeID is a GNU build id as tables keep it: lower-case hex, 4 to 64 bytes.
func validCodeID(id string) bool {
	if len(id) < 8 || len(id) > 128 || len(id)%2 != 0 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ELFID is the key of a library's table: its build id's first 16 bytes, zero-padded when
// shorter (so the symbols page shows the start of the id `readelf -n` prints). The table keeps
// the whole id in CodeID, and a frame is read against it only when the whole id matches.
func ELFID(codeID string) string {
	id := NormalizeID(codeID)
	if len(id) >= 32 {
		return id[:32]
	}
	return id + strings.Repeat("0", 32-len(id))
}

// buildID is the library's NT_GNU_BUILD_ID note, as hex.
func buildID(f *elf.File) (string, bool) {
	for _, s := range f.Sections {
		if s.Type != elf.SHT_NOTE {
			continue
		}
		b, err := s.Data()
		if err != nil {
			continue
		}
		al := 4
		if s.Addralign == 8 {
			al = 8
		}
		pad := func(n int) int { return (n + al - 1) &^ (al - 1) }
		for len(b) >= 12 {
			nameSz, descSz, typ := f.ByteOrder.Uint32(b), f.ByteOrder.Uint32(b[4:]), f.ByteOrder.Uint32(b[8:])
			name0 := 12
			desc0 := name0 + pad(int(nameSz))
			end := desc0 + pad(int(descSz))
			if nameSz > 64 || descSz > 1024 || desc0+int(descSz) > len(b) {
				break
			}
			if typ == 3 && nameSz == 4 && string(b[name0:name0+3]) == "GNU" { // NT_GNU_BUILD_ID
				return hex.EncodeToString(b[desc0 : desc0+int(descSz)]), true
			}
			if end > len(b) {
				break
			}
			b = b[end:]
		}
	}
	return "", false
}

func elfArch(m elf.Machine) string {
	switch m {
	case elf.EM_AARCH64:
		return "arm64"
	case elf.EM_ARM:
		return "arm"
	case elf.EM_X86_64:
		return "x86_64"
	case elf.EM_386:
		return "x86"
	case elf.EM_RISCV:
		return "riscv64"
	}
	return strings.ToLower(strings.TrimPrefix(m.String(), "EM_"))
}

// IsELF is whether the file at p starts as an ELF file does.
func IsELF(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var magic [4]byte
	_, err = io.ReadFull(f, magic[:])
	return err == nil && string(magic[:]) == elf.ELFMAG
}

// ExtractELFPath is the table of one unstripped ELF library on disk.
func ExtractELFPath(p string) (*Table, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ExtractELF(f, filepath.Base(p))
}

// ExtractELF is the table of one ELF shared library: its functions from its DWARF, then the
// functions its symbol table (.symtab) has that the DWARF does not cover. A stripped library,
// with only its dynamic symbols, makes no table: the device's unwinder names those already, so
// a table would read nothing more and hide that the library's symbols are missing.
func ExtractELF(r io.ReaderAt, codeFile string) (*Table, error) {
	f, err := elf.NewFile(r)
	if err != nil {
		return nil, err
	}
	id, ok := buildID(f)
	if !ok || !validCodeID(id) {
		return nil, errors.New("no GNU build id (link with -Wl,--build-id)")
	}
	t := &Table{V: Version, Kind: KindELF, DebugID: ELFID(id), CodeID: id, Arch: elfArch(f.Machine), CodeFile: codeFile}
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
		if err := fromDWARF(d, t, name, "::"); err != nil {
			return nil, err
		}
	}
	fromELFSymtab(f, t, name)
	if len(t.Funcs) == 0 {
		return nil, errors.New("stripped: no DWARF or symbol table (only an unstripped library makes a table)")
	}
	t.sortRows()
	return t, nil
}

// fromELFSymtab adds the functions of the symbol table (.symtab) that the DWARF does not
// cover, each over its own size.
func fromELFSymtab(f *elf.File, t *Table, name func(string) uint64) {
	syms, err := f.Symbols()
	if err != nil {
		return
	}
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
	seen := map[uint64]bool{}
	for _, s := range syms {
		if elf.ST_TYPE(s.Info) != elf.STT_FUNC || s.Section == elf.SHN_UNDEF || s.Size == 0 || s.Name == "" {
			continue
		}
		lo := s.Value
		if f.Machine == elf.EM_ARM {
			lo &^= 1 // a Thumb function's address has its low bit set
		}
		if seen[lo] || in(lo) {
			continue
		}
		seen[lo] = true
		t.Funcs = append(t.Funcs, [3]uint64{lo, lo + s.Size, name(s.Name)})
	}
}

// checkELF is Decode's check of a library's table.
func (t *Table) checkELF() error {
	t.CodeID = NormalizeID(t.CodeID)
	if !validCodeID(t.CodeID) || t.DebugID != ELFID(t.CodeID) {
		return fmt.Errorf("%w: a library's debug_id is not its build id's", ErrInvalid)
	}
	if len(t.Funcs) == 0 {
		return fmt.Errorf("%w: no functions", ErrInvalid)
	}
	t.Classes, t.Release, t.TextAddr = nil, "", 0
	t.sortRows()
	return nil
}
