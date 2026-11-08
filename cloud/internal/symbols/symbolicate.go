package symbols

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Frame is one crash frame as the service admin reads it: as the app sent it, and its
// place in the code when the binary's table is here.
type Frame struct {
	Raw    string  `json:"raw"`
	InApp  bool    `json:"in_app"`
	Symbol *Symbol `json:"symbol,omitempty"`
	// Inlined is a frame R8 inlined into the next one: both are one frame of the crash, so
	// only the first of such a chain has Raw
	Inlined bool `json:"inlined,omitempty"`
}

// Crash is one of a report's crashes (a Sentry event, 53.14), top frame first.
type Crash struct {
	Type   string  `json:"type"`
	Value  string  `json:"value,omitempty"`
	Frames []Frame `json:"frames"`
	// Missing are the binaries its frames name whose tables are not here (their
	// release's dSYMs were never uploaded), the release's mapping (Kind KindR8), or the app's
	// own native libraries whose tables are not here (Kind KindELF)
	Missing []Image `json:"missing,omitempty"`
	// Symbols is how its frames read: SymbolsDSYM, SymbolsMapping or SymbolsNative when a
	// table read some, SymbolsSource when the app is not obfuscated (its frames are its code's
	// own names), "" when nothing could
	Symbols string `json:"symbols,omitempty"`
}

// How a crash's frames read (Crash.Symbols).
const (
	SymbolsDSYM    = "dsym"
	SymbolsMapping = "mapping"
	SymbolsSource  = "source"
	SymbolsNative  = "native"
)

// Image is a binary a crash names, an Android release's mapping (Kind KindR8: CodeFile is
// the app, Version its release), or an Android app's native library (Kind KindELF: CodeID its
// whole build id, "" when the crash did not carry it).
type Image struct {
	CodeFile string `json:"code_file"`
	DebugID  string `json:"debug_id"`
	Kind     string `json:"kind,omitempty"`
	Version  string `json:"version,omitempty"`
	CodeID   string `json:"code_id,omitempty"`
}

// Release is the app and version a report came from: an Android crash is read against
// their mapping.
type Release struct {
	App     string
	Version string
}

// obfuscated is whether an app's release builds are shrunk by R8 (the Android apps, whose
// report app is "android.player", "android.remote" or "android.tv"). The desktop player's
// installers are built without ProGuard (packageMsi, packageDeb, packageRpm), so its frames
// already read as its code.
func obfuscated(app string) bool { return strings.HasPrefix(app, "android.") }

// Finder is where tables are looked up by debug id (nil, nil: none).
type Finder interface {
	Find(ctx context.Context, debugID string) (*Table, error)
}

// event is the part of a Sentry event read here.
type event struct {
	Platform  string `json:"platform"`
	Exception struct {
		Values []struct {
			Type       string `json:"type"`
			Value      string `json:"value"`
			Stacktrace struct {
				Frames []struct {
					Function string `json:"function"`
					Package  string `json:"package"`
					InApp    bool   `json:"in_app"`
					// a JVM frame's file and line (the Android and desktop apps)
					Filename string `json:"filename"`
					Lineno   int    `json:"lineno"`
					// a native frame's pc in its library ("0x1a2b") and the library
					// ("rel:<index into debug_meta.images>"; the Android apps' tombstones)
					InstructionAddr string `json:"instruction_addr"`
					AddrMode        string `json:"addr_mode"`
				} `json:"frames"`
			} `json:"stacktrace"`
		} `json:"values"`
	} `json:"exception"`
	DebugMeta struct {
		Images []struct {
			Type     string `json:"type"`
			CodeFile string `json:"code_file"`
			DebugID  string `json:"debug_id"`
			CodeID   string `json:"code_id"` // an ELF library's GNU build id
		} `json:"images"`
	} `json:"debug_meta"`
}

// MaxCrashes is the most crashes read from one report (the home keeps 5).
const MaxCrashes = 5

// Symbolicate reads a report's crashes against the tables find has. A crash that is not an
// event is skipped; a frame whose binary has no table keeps only its raw form. Tables are
// looked up once each. An Apple crash names its binaries' UUIDs; a JVM crash (platform
// "java") of an obfuscated app is read against rel's mapping, and one of an app that is not
// obfuscated is marked as read already; an Android native crash (platform "native") names its
// libraries' build ids.
func Symbolicate(ctx context.Context, crashes []json.RawMessage, find Finder, rel Release) []Crash {
	tables := map[string]*Table{}
	looked := map[string]bool{}
	table := func(id string) *Table {
		if !looked[id] {
			looked[id] = true
			if find != nil {
				if t, err := find.Find(ctx, id); err == nil {
					tables[id] = t
				}
			}
		}
		return tables[id]
	}
	out := []Crash{}
	for i, raw := range crashes {
		if i >= MaxCrashes {
			break
		}
		var e event
		if json.Unmarshal(raw, &e) != nil || len(e.Exception.Values) == 0 {
			continue
		}
		images := map[string]string{}
		for _, im := range e.DebugMeta.Images {
			if id := NormalizeID(im.DebugID); im.CodeFile != "" && validID(id) {
				images[im.CodeFile] = id
			}
		}
		ex := e.Exception.Values[0]
		switch e.Platform {
		case "java":
			out = append(out, symbolicateJVM(e, rel, table))
			continue
		case "native":
			out = append(out, symbolicateNative(e, table))
			continue
		}
		c := Crash{Type: ex.Type, Value: ex.Value, Frames: []Frame{}}
		missing := map[string]bool{}
		fr := ex.Stacktrace.Frames
		// Sentry's frames are oldest first; the admin reads the top first
		for k := len(fr) - 1; k >= 0; k-- {
			f := fr[k]
			x := Frame{Raw: f.Function, InApp: f.InApp}
			bin, off, ok := splitFrame(f.Function, f.Package)
			if id := images[bin]; ok && id != "" {
				if t := table(id); t != nil {
					// a caller's frame is its return address: the call is the byte before it
					if k != len(fr)-1 && off > 0 {
						off--
					}
					if s, found := t.Lookup(off); found {
						x.Symbol = &s
						c.Symbols = SymbolsDSYM
					}
				} else if !missing[id] {
					missing[id] = true
					c.Missing = append(c.Missing, Image{CodeFile: bin, DebugID: id})
				}
			}
			c.Frames = append(c.Frames, x)
		}
		out = append(out, c)
	}
	return out
}

// symbolicateJVM reads a Java or Kotlin crash: frames "pkg.Class.method" with their file and
// line. An obfuscated app's are read against its release's mapping (table looks it up by
// R8ID); another app's frames are its code's names already.
func symbolicateJVM(e event, rel Release, table func(string) *Table) Crash {
	ex := e.Exception.Values[0]
	c := Crash{Type: ex.Type, Value: ex.Value, Frames: []Frame{}}
	var t *Table
	if obfuscated(rel.App) {
		id := R8ID(rel.App, rel.Version)
		if t = table(id); t == nil || t.Kind != KindR8 {
			t = nil
			c.Missing = []Image{{CodeFile: rel.App, DebugID: id, Kind: KindR8, Version: rel.Version}}
		}
	} else {
		c.Symbols = SymbolsSource
	}
	if t != nil {
		c.Symbols = SymbolsMapping
		if name, ok := t.RetraceClass(c.Type); ok {
			if c.Value == c.Type {
				c.Value = name
			}
			c.Type = name
		}
	}
	fr := ex.Stacktrace.Frames
	if len(fr) == 0 {
		// only the reason was kept (an Android native crash before Android 12, or a tombstone
		// that could not be read): there is nothing to read, and no mapping is missing
		c.Missing, c.Symbols = nil, ""
		return c
	}
	for k := len(fr) - 1; k >= 0; k-- {
		f := fr[k]
		raw := f.Function
		if f.Lineno > 0 {
			raw = fmt.Sprintf("%s(%s:%d)", f.Function, orUnknown(f.Filename), f.Lineno)
		}
		x := Frame{Raw: raw, InApp: f.InApp}
		var chain []Symbol
		if t != nil {
			if i := strings.LastIndexByte(f.Function, '.'); i > 0 {
				chain = t.Retrace(f.Function[:i], f.Function[i+1:], f.Lineno)
			}
		}
		if len(chain) == 0 {
			c.Frames = append(c.Frames, x)
			continue
		}
		for j, s := range chain {
			s := s
			y := Frame{InApp: f.InApp, Symbol: &s, Inlined: j < len(chain)-1}
			if j == 0 {
				y.Raw = raw
			}
			c.Frames = append(c.Frames, y)
		}
	}
	return c
}

// symbolicateNative reads an Android native crash: each frame's pc relative to its library,
// the library named by addr_mode "rel:N" (debug_meta.images[N], with its build id). The app's
// own libraries (in_app) are read against their tables; a system library's frames stay as
// they came, and only an own library without a table is named missing.
func symbolicateNative(e event, table func(string) *Table) Crash {
	ex := e.Exception.Values[0]
	c := Crash{Type: ex.Type, Value: ex.Value, Frames: []Frame{}}
	images := make([]nativeImage, len(e.DebugMeta.Images))
	for i, im := range e.DebugMeta.Images {
		images[i] = nativeImage{file: clip(path.Base(im.CodeFile), 200)}
		if id := NormalizeID(im.CodeID); validCodeID(id) {
			images[i].codeID = id
		}
	}
	missing := map[string]bool{}
	fr := ex.Stacktrace.Frames
	for k := len(fr) - 1; k >= 0; k-- {
		f := fr[k]
		x := Frame{Raw: f.Function, InApp: f.InApp}
		im, addr, ok := nativeAddr(f.AddrMode, f.InstructionAddr, images)
		if ok && f.InApp {
			// a tombstone's caller frames are already the call's pc (the unwinder steps back
			// over the return address), so no frame is moved back a byte here
			var t *Table
			if im.codeID != "" {
				if t = table(ELFID(im.codeID)); t != nil && (t.Kind != KindELF || t.CodeID != im.codeID) {
					t = nil
				}
			}
			if t != nil {
				if s, found := t.Lookup(addr); found {
					x.Symbol = &s
					c.Symbols = SymbolsNative
				}
			} else if key := im.file + "\x00" + im.codeID; !missing[key] {
				missing[key] = true
				m := Image{CodeFile: im.file, Kind: KindELF, CodeID: im.codeID}
				if im.codeID != "" {
					m.DebugID = ELFID(im.codeID)
				}
				c.Missing = append(c.Missing, m)
			}
		}
		c.Frames = append(c.Frames, x)
	}
	return c
}

// nativeImage is a native crash's library: its file name and build id ("" when unknown).
type nativeImage struct{ file, codeID string }

// nativeAddr is a native frame's library (addr_mode "rel:N") and pc in it ("0x1a2b").
func nativeAddr(mode, addr string, images []nativeImage) (nativeImage, uint64, bool) {
	var none nativeImage
	n, ok := strings.CutPrefix(mode, "rel:")
	if !ok {
		return none, 0, false
	}
	i, err := strconv.Atoi(n)
	if err != nil || i < 0 || i >= len(images) {
		return none, 0, false
	}
	hexAddr, ok := strings.CutPrefix(strings.ToLower(addr), "0x")
	if !ok {
		return none, 0, false
	}
	a, err := strconv.ParseUint(hexAddr, 16, 64)
	if err != nil {
		return none, 0, false
	}
	return images[i], a, true
}

func orUnknown(file string) string {
	if file == "" {
		return "Unknown Source"
	}
	return file
}

// splitFrame reads "Binary Name + 0x1a2b" (the apps' unsymbolicated frame) as its binary
// and offset.
func splitFrame(function, pkg string) (string, uint64, bool) {
	i := strings.LastIndex(function, " + 0x")
	if i <= 0 {
		return "", 0, false
	}
	off, err := strconv.ParseUint(function[i+5:], 16, 64)
	if err != nil {
		return "", 0, false
	}
	bin := function[:i]
	if pkg != "" && pkg != bin {
		return "", 0, false
	}
	return bin, off, true
}
