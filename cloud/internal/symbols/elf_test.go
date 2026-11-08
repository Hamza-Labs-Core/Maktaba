package symbols

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/symbols/symbolstest"
)

// native.so is testdata/build-native.sh's: native.c (mpv_render, the static decode_frame with
// clamp inlined into it) with DWARF, nodebug.c's scale_nodebug without, an aarch64 shared
// library with a sha1 GNU build id; native.stripped.so is it stripped.

func nativeTable(t *testing.T) *Table {
	t.Helper()
	tab, err := ExtractELFPath(symbolstest.Fixture(t, "native.so"))
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

// funcAt is a function's [low, high) in the library's address space.
func funcAt(t *testing.T, tab *Table, fn string) (uint64, uint64) {
	t.Helper()
	i := slices.Index(tab.Names, fn)
	for _, f := range tab.Funcs {
		if int(f[2]) == i {
			return f[0], f[1]
		}
	}
	t.Fatalf("%s is not in the table (%v)", fn, tab.Names)
	return 0, 0
}

func TestExtractELF(t *testing.T) {
	tab := nativeTable(t)
	if tab.Kind != KindELF || tab.Arch != "arm64" || tab.CodeFile != "native.so" || tab.TextAddr != 0 {
		t.Fatalf("table = kind %q arch %q file %q text %#x", tab.Kind, tab.Arch, tab.CodeFile, tab.TextAddr)
	}
	// a sha1 build id: 20 bytes, keyed by its first 16
	if len(tab.CodeID) != 40 || !validCodeID(tab.CodeID) || tab.DebugID != tab.CodeID[:32] || !validID(tab.DebugID) {
		t.Fatalf("build id %q, debug id %q", tab.CodeID, tab.DebugID)
	}
	for _, fn := range []string{"mpv_render", "decode_frame", "scale_nodebug"} {
		lo, hi := funcAt(t, tab, fn)
		if hi <= lo {
			t.Errorf("%s: [%#x, %#x)", fn, lo, hi)
		}
	}
	// the exported function, with its file and line (the null pointer it reads is line 17)
	lo, hi := funcAt(t, tab, "mpv_render")
	s, ok := tab.Lookup(lo)
	if !ok || s.Function != "mpv_render" || s.File != "/src/native.c" || s.Line < 15 || s.Line > 18 {
		t.Errorf("mpv_render's first byte = %+v %v", s, ok)
	}
	if s, ok := tab.Lookup(hi - 4); !ok || s.Function != "mpv_render" {
		t.Errorf("mpv_render's last instruction = %+v %v", s, ok)
	}
	// the static function: clamp is inlined into it, so its lines include clamp's (line 4)
	lo, hi = funcAt(t, tab, "decode_frame")
	lines := map[int]bool{}
	for a := lo; a < hi; a += 4 {
		s, ok := tab.Lookup(a)
		if !ok || s.Function != "decode_frame" || s.File != "/src/native.c" {
			t.Fatalf("decode_frame+%#x = %+v %v", a-lo, s, ok)
		}
		lines[s.Line] = true
	}
	if !lines[4] || !lines[9] {
		t.Errorf("decode_frame's lines %v: want clamp's (4) and the loop's (9)", lines)
	}
	// built without debug info: the symbol table names it, with no file
	lo, _ = funcAt(t, tab, "scale_nodebug")
	if s, ok := tab.Lookup(lo); !ok || s.Function != "scale_nodebug" || s.File == "/src/native.c" {
		t.Errorf("scale_nodebug = %+v %v", s, ok)
	}
	if _, ok := tab.Lookup(0); ok {
		t.Error("address 0 is in no function")
	}
	// small: ranges and lines, not the DWARF
	b, err := tab.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 4096 {
		t.Errorf("encoded table is %d bytes", len(b))
	}
}

func TestExtractELFRefuses(t *testing.T) {
	if _, err := ExtractELF(bytes.NewReader([]byte("not an elf file")), "x.so"); err == nil {
		t.Error("a file that is not ELF")
	}
	// a Mach-O is not ELF either
	if IsELF(symbolstest.Fixture(t, "crashy.arm64")) {
		t.Error("IsELF(Mach-O)")
	}
	if !IsELF(symbolstest.Fixture(t, "native.so")) || IsELF("/nonexistent") {
		t.Error("IsELF")
	}
	// the library as the release ships it: only its dynamic symbols, which the device's
	// unwinder names already, so no table
	if _, err := ExtractELFPath(symbolstest.Fixture(t, "native.stripped.so")); err == nil || !strings.Contains(err.Error(), "stripped") {
		t.Errorf("a stripped library: %v", err)
	}
	// the same library without its build id note: no key to find it by
	raw, err := os.ReadFile(symbolstest.Fixture(t, "native.so"))
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(raw, []byte("GNU\x00"))
	if i < 12 {
		t.Fatal("no GNU note in the fixture")
	}
	noID := slices.Clone(raw)
	noID[i-4] = 0x7f // the note's type: no longer NT_GNU_BUILD_ID
	if _, err := ExtractELF(bytes.NewReader(noID), "native.so"); err == nil || !strings.Contains(err.Error(), "build id") {
		t.Errorf("no build id: %v", err)
	}
}

func TestELFID(t *testing.T) {
	if got := ELFID("76F92408CF4FE3EDA298799C5207165DEEB64261"); got != "76f92408cf4fe3eda298799c5207165d" {
		t.Errorf("sha1 id → %q", got)
	}
	if got := ELFID("0123456789abcdef"); got != "0123456789abcdef0000000000000000" {
		t.Errorf("an 8-byte id → %q", got)
	}
	for id, ok := range map[string]bool{"0123abcd": true, "0123abc": false, "012": false, "zz23abcd": false,
		strings.Repeat("ab", 64): true, strings.Repeat("ab", 65): false} {
		if validCodeID(id) != ok {
			t.Errorf("validCodeID(%q) = %v", id, !ok)
		}
	}
}

func TestELFEncodeDecode(t *testing.T) {
	tab := nativeTable(t)
	b, err := tab.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindELF || got.CodeID != tab.CodeID || got.DebugID != tab.DebugID || len(got.Funcs) != len(tab.Funcs) {
		t.Fatalf("round trip: %+v", got)
	}
	lo, _ := funcAt(t, got, "decode_frame")
	if s, ok := got.Lookup(lo + 4); !ok || s.Function != "decode_frame" {
		t.Errorf("after the round trip: %+v %v", s, ok)
	}
	// a library's key must be its build id's
	for name, mut := range map[string]func(*Table){
		"another key":   func(x *Table) { x.DebugID = strings.Repeat("a", 32) },
		"no build id":   func(x *Table) { x.CodeID = "" },
		"a bad id":      func(x *Table) { x.CodeID = "xyz" },
		"no functions":  func(x *Table) { x.Funcs = nil },
		"another build": func(x *Table) { x.CodeID = strings.Repeat("b", 40) },
	} {
		x := *tab
		mut(&x)
		b, err := x.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

// nativeEvent is an Android native crash as Envelope.kt sends it: frames oldest first, each
// a library (addr_mode rel:N) and its pc in it.
func nativeEvent(frames []map[string]any, images []map[string]any) json.RawMessage {
	ev := map[string]any{
		"platform": "native",
		"exception": map[string]any{"values": []any{map[string]any{"type": "SIGSEGV", "value": "SEGV_MAPERR",
			"mechanism":  map[string]any{"type": "native", "handled": false},
			"stacktrace": map[string]any{"frames": frames}}}},
		"debug_meta": map[string]any{"images": images},
	}
	b, _ := json.Marshal(ev)
	return b
}

func nativeFrame(fn, module string, pc uint64, image int, inApp bool) map[string]any {
	return map[string]any{"function": fn, "package": module, "instruction_addr": fmt.Sprintf("0x%x", pc),
		"addr_mode": fmt.Sprintf("rel:%d", image), "in_app": inApp}
}

func TestSymbolicateNative(t *testing.T) {
	tab := nativeTable(t)
	render, _ := funcAt(t, tab, "mpv_render")
	decode, _ := funcAt(t, tab, "decode_frame")
	ev := nativeEvent([]map[string]any{
		nativeFrame("__libc_init+0x60", "libc.so", 0x4a1b4, 0, false),
		nativeFrame("native.so + 0x"+fmt.Sprintf("%x", render+8), "native.so", render+8, 1, true),
		nativeFrame("decode_frame+0x8", "native.so", decode+8, 1, true),
		nativeFrame("libother.so + 0x10", "libother.so", 0x10, 2, true),
		nativeFrame("libother.so + 0x20", "libother.so", 0x20, 2, true),
		nativeFrame("libnoid.so + 0x30", "libnoid.so", 0x30, 3, true),
	}, []map[string]any{
		{"type": "elf", "code_file": "libc.so", "code_id": "ffeeddccbbaa99887766554433221100"},
		{"type": "elf", "code_file": "native.so", "code_id": strings.ToUpper(tab.CodeID)},
		{"type": "elf", "code_file": "libother.so", "code_id": "0102030405060708090a0b0c0d0e0f10aabbccdd"},
		{"type": "elf", "code_file": "libnoid.so"},
	})
	got := Symbolicate(context.Background(), []json.RawMessage{ev}, finder{tab.DebugID: tab}, Release{App: "android.player", Version: "0.3.103"})
	if len(got) != 1 {
		t.Fatalf("crashes = %d", len(got))
	}
	c := got[0]
	if c.Type != "SIGSEGV" || c.Symbols != SymbolsNative || len(c.Frames) != 6 {
		t.Fatalf("crash = %+v", c)
	}
	// top first: the unreadable own libraries, then native.so's two, then libc's
	if f := c.Frames[0]; f.Symbol != nil || f.Raw != "libnoid.so + 0x30" || !f.InApp {
		t.Errorf("top = %+v", f)
	}
	if f := c.Frames[3]; f.Symbol == nil || f.Symbol.Function != "decode_frame" || f.Symbol.File != "/src/native.c" ||
		f.Raw != "decode_frame+0x8" {
		t.Errorf("decode_frame = %+v", f)
	}
	if f := c.Frames[4]; f.Symbol == nil || f.Symbol.Function != "mpv_render" || f.Symbol.Line == 0 {
		t.Errorf("mpv_render = %+v", f)
	}
	// a system library stays as it came and is never named missing
	if f := c.Frames[5]; f.Symbol != nil || f.Raw != "__libc_init+0x60" || f.InApp {
		t.Errorf("libc = %+v", f)
	}
	// each own library without a table named once, with its build id (or none)
	if len(c.Missing) != 2 {
		t.Fatalf("missing = %+v", c.Missing)
	}
	if m := c.Missing[0]; m.CodeFile != "libnoid.so" || m.Kind != KindELF || m.CodeID != "" || m.DebugID != "" {
		t.Errorf("missing[0] = %+v", m)
	}
	if m := c.Missing[1]; m.CodeFile != "libother.so" || m.Kind != KindELF ||
		m.CodeID != "0102030405060708090a0b0c0d0e0f10aabbccdd" || m.DebugID != "0102030405060708090a0b0c0d0e0f10" {
		t.Errorf("missing[1] = %+v", m)
	}

	// a table under the key whose whole build id differs (a shared 16-byte prefix) is not read
	other := *tab
	other.CodeID = tab.DebugID + "00000000"
	got = Symbolicate(context.Background(), []json.RawMessage{ev}, finder{tab.DebugID: &other}, Release{})
	if c := got[0]; c.Symbols != "" || c.Frames[4].Symbol != nil || len(c.Missing) != 3 {
		t.Errorf("another build's table: %+v", c)
	}
	// nor a dSYM's table that happens to have the key
	apple := byArch(t, extract(t, "crashy.dwarf"), "arm64")
	got = Symbolicate(context.Background(), []json.RawMessage{ev}, finder{tab.DebugID: apple}, Release{})
	if c := got[0]; c.Symbols != "" || c.Frames[4].Symbol != nil {
		t.Errorf("a dSYM's table: %+v", c)
	}
}

func TestSymbolicateNativeOddFrames(t *testing.T) {
	tab := nativeTable(t)
	render, _ := funcAt(t, tab, "mpv_render")
	images := []map[string]any{{"type": "elf", "code_file": "/data/app/x/lib/arm64/native.so", "code_id": tab.CodeID}}
	for name, f := range map[string]map[string]any{
		"no addr_mode":      {"function": "f", "instruction_addr": fmt.Sprintf("0x%x", render), "in_app": true},
		"an image past":     nativeFrame("f", "native.so", render, 5, true),
		"a negative image":  nativeFrame("f", "native.so", render, -1, true),
		"an absolute mode":  {"function": "f", "instruction_addr": fmt.Sprintf("0x%x", render), "addr_mode": "abs", "in_app": true},
		"no hex prefix":     {"function": "f", "instruction_addr": fmt.Sprintf("%x", render), "addr_mode": "rel:0", "in_app": true},
		"a bad address":     {"function": "f", "instruction_addr": "0xnothex", "addr_mode": "rel:0", "in_app": true},
		"an empty address":  {"function": "f", "addr_mode": "rel:0", "in_app": true},
		"not the app's own": nativeFrame("f", "native.so", render, 0, false),
	} {
		got := Symbolicate(context.Background(), []json.RawMessage{nativeEvent([]map[string]any{f}, images)}, finder{tab.DebugID: tab}, Release{})
		if len(got) != 1 || len(got[0].Frames) != 1 || got[0].Frames[0].Symbol != nil || got[0].Frames[0].Raw != "f" ||
			len(got[0].Missing) != 0 || got[0].Symbols != "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	// a path in code_file is cut to its file name; the frame still reads
	got := Symbolicate(context.Background(), []json.RawMessage{nativeEvent([]map[string]any{
		nativeFrame("f", "native.so", render, 0, true)}, images)}, finder{tab.DebugID: tab}, Release{})
	if f := got[0].Frames[0]; f.Symbol == nil || f.Symbol.Function != "mpv_render" {
		t.Errorf("a path's library: %+v", got[0])
	}
	got = Symbolicate(context.Background(), []json.RawMessage{nativeEvent([]map[string]any{
		nativeFrame("f", "native.so", render, 0, true)}, images)}, nil, Release{})
	if m := got[0].Missing; len(m) != 1 || m[0].CodeFile != "native.so" {
		t.Errorf("missing with a path: %+v", m)
	}
}

// An Android native crash with only its reason (Android 11, or a tombstone that could not be
// read) comes as platform "java" with no frames: nothing to read, and no mapping is asked for.
func TestAReasonOnlyNativeCrashAsksForNothing(t *testing.T) {
	ev, _ := json.Marshal(map[string]any{
		"platform": "java",
		"exception": map[string]any{"values": []any{map[string]any{"type": "native", "value": "crash (signal 11)",
			"mechanism":  map[string]any{"type": "native", "handled": false},
			"stacktrace": map[string]any{"frames": []any{}}}}},
	})
	got := Symbolicate(context.Background(), []json.RawMessage{ev}, finder{}, Release{App: "android.tv", Version: "0.3.103"})
	if len(got) != 1 || len(got[0].Frames) != 0 || got[0].Frames == nil || len(got[0].Missing) != 0 || got[0].Symbols != "" ||
		got[0].Value != "crash (signal 11)" {
		t.Fatalf("reason only: %+v", got)
	}
	// and a native one with no frames either
	got = Symbolicate(context.Background(), []json.RawMessage{nativeEvent(nil, nil)}, finder{}, Release{})
	if len(got) != 1 || len(got[0].Frames) != 0 || got[0].Frames == nil || len(got[0].Missing) != 0 {
		t.Fatalf("native, no frames: %+v", got)
	}
}
