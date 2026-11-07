package symbols

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/symbols/symbolstest"
)

// The fixtures are built by testdata/build.sh: crashy.c (fat: arm64 and x86_64) and
// player.swift (arm64), their sources mapped to /src, kept gzip'd (symbolstest unpacks them).

func extract(t *testing.T, name string) []*Table {
	t.Helper()
	ts, err := ExtractPath(symbolstest.Fixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func byArch(t *testing.T, ts []*Table, arch string) *Table {
	t.Helper()
	for _, x := range ts {
		if x.Arch == arch {
			return x
		}
	}
	t.Fatalf("no %s table in %d", arch, len(ts))
	return nil
}

// offsetOf is a function's first byte, as an offset into __TEXT.
func offsetOf(t *testing.T, tab *Table, fn string) uint64 {
	t.Helper()
	i := slices.Index(tab.Names, fn)
	for _, f := range tab.Funcs {
		if int(f[2]) == i {
			return f[0] - tab.TextAddr
		}
	}
	t.Fatalf("%s is not in the table (%v)", fn, tab.Names)
	return 0
}

func TestExtractFatDSYM(t *testing.T) {
	ts := extract(t, "crashy.dwarf")
	if len(ts) != 2 {
		t.Fatalf("tables = %d, want one per architecture", len(ts))
	}
	arm, x86 := byArch(t, ts, "arm64"), byArch(t, ts, "x86_64")
	if !validID(arm.DebugID) || !validID(x86.DebugID) || arm.DebugID == x86.DebugID {
		t.Fatalf("debug ids %q %q", arm.DebugID, x86.DebugID)
	}
	for _, tab := range ts {
		if tab.CodeFile != "crashy.dwarf" || tab.TextAddr != 0x100000000 {
			t.Errorf("%s: code file %q, __TEXT at %#x", tab.Arch, tab.CodeFile, tab.TextAddr)
		}
		depth := offsetOf(t, tab, "depth")
		s, ok := tab.Lookup(depth + 8)
		if !ok || s.Function != "depth" || s.File != "/src/crashy.c" || s.Line != 2 {
			t.Errorf("%s: depth+8 = %+v %v", tab.Arch, s, ok)
		}
		s, ok = tab.Lookup(offsetOf(t, tab, "play_item"))
		if !ok || s.Function != "play_item" || s.Line != 3 {
			t.Errorf("%s: play_item = %+v %v", tab.Arch, s, ok)
		}
		if s, ok := tab.Lookup(0); ok {
			t.Errorf("%s: the Mach-O header is %+v", tab.Arch, s)
		}
		if s, ok := tab.Lookup(1 << 30); ok {
			t.Errorf("%s: past the code is %+v", tab.Arch, s)
		}
	}
}

func TestExtractSwiftNamesWithTheirTypes(t *testing.T) {
	tab := byArch(t, extract(t, "player.dwarf"), "arm64")
	for _, want := range []string{"Demo.Player.play", "Demo.App.main"} {
		if !slices.Contains(tab.Names, want) {
			t.Errorf("%s missing from %v", want, tab.Names)
		}
	}
	s, ok := tab.Lookup(offsetOf(t, tab, "Demo.Player.play") + 4)
	if !ok || s.Function != "Demo.Player.play" || s.File != "/src/player.swift" || s.Line != 1 {
		t.Errorf("play = %+v %v", s, ok)
	}
}

func TestExtractSymbolTableWithoutDWARF(t *testing.T) {
	tab := byArch(t, extract(t, "crashy.arm64"), "arm64")
	for _, fn := range []string{"depth", "play_item", "main"} {
		s, ok := tab.Lookup(offsetOf(t, tab, fn) + 4)
		if !ok || s.Function != fn || s.File != "" {
			t.Errorf("%s: %+v %v", fn, s, ok)
		}
	}
}

func TestExtractDSYMBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Crashy.app.dSYM")
	dwarf := filepath.Join(dir, "Contents", "Resources", "DWARF")
	if err := os.MkdirAll(dwarf, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(symbolstest.Fixture(t, "crashy.dwarf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dwarf, "Crashy"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	ts, err := ExtractPath(dir)
	if err != nil || len(ts) != 2 || ts[0].CodeFile != "Crashy" {
		t.Fatalf("bundle: %d tables, %v", len(ts), err)
	}
	if _, err := ExtractPath(t.TempDir()); err == nil {
		t.Error("an empty directory has tables")
	}
	if _, err := ExtractPath(filepath.Join("testdata", "crashy.c")); err == nil {
		t.Error("a C file has tables")
	}
}

func TestEncodeDecode(t *testing.T) {
	tab := byArch(t, extract(t, "crashy.dwarf"), "arm64")
	b, err := tab.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.DebugID != tab.DebugID || len(got.Funcs) != len(tab.Funcs) || len(got.Lines) != len(tab.Lines) {
		t.Fatalf("round trip: %+v", got)
	}
	if s, ok := got.Lookup(offsetOf(t, tab, "main")); !ok || s.Function != "main" {
		t.Errorf("decoded lookup: %+v", s)
	}
}

func gz(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestDecodeRefuses(t *testing.T) {
	good := Table{V: Version, DebugID: "9E40F33C-5E1E-3752-8634-A1CD1B5AE44F", Arch: "arm64", CodeFile: "../../X",
		Names: []string{"f"}, Funcs: [][3]uint64{{0x100, 0x110, 0}}}
	if got, err := Decode(gz(t, good)); err != nil || got.DebugID != "9e40f33c5e1e37528634a1cd1b5ae44f" || got.CodeFile != "X" {
		t.Fatalf("good: %+v %v", got, err)
	}
	cases := map[string][]byte{
		"not gzip": []byte(`{"v":1}`),
		"not json": func() []byte {
			var b bytes.Buffer
			zw := gzip.NewWriter(&b)
			_, _ = zw.Write([]byte("x"))
			_ = zw.Close()
			return b.Bytes()
		}(),
		"version":      gz(t, func() Table { x := good; x.V = 2; return x }()),
		"debug id":     gz(t, func() Table { x := good; x.DebugID = "not-a-uuid"; return x }()),
		"no functions": gz(t, func() Table { x := good; x.Funcs = nil; return x }()),
	}
	for name, b := range cases {
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

// a Sentry event as the Apple apps send one (Envelope.swift): frames oldest first, each
// "<binary> + 0x<offset into __TEXT>", the binaries' UUIDs in debug_meta
func crashEvent(frames [][2]any, images map[string]string) json.RawMessage {
	fs := []map[string]any{}
	for _, f := range frames {
		fs = append(fs, map[string]any{"function": fmt.Sprintf("%s + 0x%x", f[0], f[1]), "package": f[0],
			"in_app": f[0] == "crashy"})
	}
	ims := []map[string]any{}
	for k, v := range images {
		ims = append(ims, map[string]any{"type": "macho", "code_file": k, "debug_id": v})
	}
	b, _ := json.Marshal(map[string]any{
		"platform": "cocoa",
		"exception": map[string]any{"values": []any{map[string]any{"type": "EXC_BAD_ACCESS", "value": "SIGSEGV",
			"stacktrace": map[string]any{"frames": fs}}}},
		"debug_meta": map[string]any{"images": ims},
	})
	return b
}

type finder map[string]*Table

func (f finder) Find(_ context.Context, id string) (*Table, error) { return f[id], nil }

func TestSymbolicate(t *testing.T) {
	tab := byArch(t, extract(t, "crashy.dwarf"), "arm64")
	// MetricKit's UUIDs are upper case with dashes
	id := strings.ToUpper(tab.DebugID[:8] + "-" + tab.DebugID[8:12] + "-" + tab.DebugID[12:16] + "-" +
		tab.DebugID[16:20] + "-" + tab.DebugID[20:])
	mainOff, depthOff := offsetOf(t, tab, "main"), offsetOf(t, tab, "depth")
	ev := crashEvent([][2]any{
		{"libdyld.dylib", 0x1d34},
		{"crashy", mainOff + 0x10},
		// a caller's frame is its return address and the call is the byte before it: a call
		// that is play_item's last instruction returns to depth's first byte
		{"crashy", depthOff},
		{"crashy", depthOff + 8},
	}, map[string]string{"crashy": id, "libdyld.dylib": "11111111-2222-3333-4444-555555555555"})
	got := Symbolicate(context.Background(), []json.RawMessage{json.RawMessage(`"not an event"`), ev}, finder{tab.DebugID: tab})
	if len(got) != 1 {
		t.Fatalf("crashes = %d, want the one event", len(got))
	}
	c := got[0]
	if c.Type != "EXC_BAD_ACCESS" || c.Value != "SIGSEGV" || len(c.Frames) != 4 {
		t.Fatalf("crash = %+v", c)
	}
	// top first
	if f := c.Frames[0]; f.Symbol == nil || f.Symbol.Function != "depth" || f.Symbol.Line != 2 || !f.InApp {
		t.Errorf("top = %+v", f)
	}
	if f := c.Frames[1]; f.Symbol == nil || f.Symbol.Function != "play_item" {
		t.Errorf("the return address one past play_item's end reads as %+v", f.Symbol)
	}
	if f := c.Frames[2]; f.Symbol == nil || f.Symbol.Function != "main" || f.Symbol.File != "/src/crashy.c" {
		t.Errorf("main = %+v", f.Symbol)
	}
	if f := c.Frames[3]; f.Symbol != nil || f.Raw != "libdyld.dylib + 0x1d34" {
		t.Errorf("a system frame = %+v", f)
	}
	if len(c.Missing) != 1 || c.Missing[0].CodeFile != "libdyld.dylib" || c.Missing[0].DebugID != "11111111222233334444555555555555" {
		t.Errorf("missing = %+v", c.Missing)
	}
	// no tables at all: raw frames, every binary missing
	got = Symbolicate(context.Background(), []json.RawMessage{ev}, nil)
	if len(got) != 1 || got[0].Frames[0].Symbol != nil || len(got[0].Missing) != 2 {
		t.Errorf("no finder: %+v", got)
	}
}

func TestSplitFrame(t *testing.T) {
	for in, want := range map[[2]string]struct {
		bin string
		off uint64
		ok  bool
	}{
		{"Maktaba Remote + 0x1a2b", "Maktaba Remote"}: {"Maktaba Remote", 0x1a2b, true},
		{"Maktaba Remote + 0x1a2b", ""}:               {"Maktaba Remote", 0x1a2b, true},
		{"Maktaba Remote + 0x1a2b", "Other"}:          {"", 0, false},
		{"play(item:)", "X"}:                          {"", 0, false},
		{"X + 0xzz", "X"}:                             {"", 0, false},
		{" + 0x10", ""}:                               {"", 0, false},
	} {
		bin, off, ok := splitFrame(in[0], in[1])
		if bin != want.bin || off != want.off || ok != want.ok {
			t.Errorf("%q: %q %#x %v", in, bin, off, ok)
		}
	}
}

func TestUploadRefuses(t *testing.T) {
	r := chi.NewRouter()
	MountUpload(r, &Deps{Store: &Store{}, Token: "tok"})
	post := func(h http.Handler, auth string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/symbols", bytes.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	off := chi.NewRouter()
	MountUpload(off, &Deps{Store: &Store{}})
	if w := post(off, "Bearer ", nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no token set up: %d", w.Code)
	}
	for _, auth := range []string{"", "Bearer nope", "Basic dG9rOg==", "tok"} {
		if w := post(r, auth, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%q: %d", auth, w.Code)
		}
	}
	if w := post(r, "Bearer tok", []byte("junk")); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_table") {
		t.Errorf("junk: %d %s", w.Code, w.Body)
	}
	if w := post(r, "Bearer tok", bytes.Repeat([]byte("x"), MaxTable+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: %d", w.Code)
	}
}

func TestAdminListNeedsTheGate(t *testing.T) {
	r := chi.NewRouter()
	MountAdmin(r, &Deps{Store: &Store{}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/admin/symbols", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("no gate: %d", w.Code)
	}
}
