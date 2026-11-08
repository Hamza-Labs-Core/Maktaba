package symbols

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// testdata/mapping.txt is an R8 mapping in its current format (2.2): source files in JSON
// comments, ranges with and without original lines, a call inlined from another class, an
// overload told apart by its lines, and a method with no line range.
func mapping(t *testing.T) *Table {
	t.Helper()
	f, err := os.Open("testdata/mapping.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	tab, err := ParseR8(f, "android.player", "v0.3.102")
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestParseR8(t *testing.T) {
	tab := mapping(t)
	if tab.Kind != KindR8 || tab.CodeFile != "android.player" || tab.Release != "0.3.102" || tab.Arch != "jvm" {
		t.Fatalf("table = %+v", tab)
	}
	// a release is one key whether its version has the tag's v or not
	if tab.DebugID != R8ID("android.player", "0.3.102") || tab.DebugID != R8ID("android.player", "v0.3.102") ||
		!validID(tab.DebugID) || tab.DebugID == R8ID("android.remote", "0.3.102") {
		t.Fatalf("debug id %q", tab.DebugID)
	}
	if len(tab.Classes) != 4 || tab.Classes[0].File != "PlaybackController.kt" {
		t.Fatalf("classes = %+v", tab.Classes)
	}
	// the field is not kept; the eight methods are
	if n := len(tab.Classes[0].Methods); n != 8 {
		t.Fatalf("methods = %d: %+v", n, tab.Classes[0].Methods)
	}
	if m := tab.Classes[0].Methods[3]; m != (R8Method{Obf: "a", Name: "play", A: 2, B: 2, S: 41}) {
		t.Errorf("an inlined caller's line = %+v", m)
	}
	for name, in := range map[string]string{
		"empty":            "",
		"member first":     "    void f() -> a\n",
		"not a mapping":    "hello\n",
		"comments only":    "# compiler: R8\n",
		"class without ->": "com.x.Y:\n",
	} {
		if _, err := ParseR8(strings.NewReader(in), "android.player", "1"); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := ParseR8(strings.NewReader("a -> b:\n"), "", "1"); err == nil {
		t.Error("no app: parsed")
	}
}

func TestRetrace(t *testing.T) {
	tab := mapping(t)
	cases := []struct {
		class, method string
		line          int
		want          []Symbol
	}{
		// a range mapped line for line
		{"a.b.c", "a", 4, []Symbol{{"com.hamzalabs.maktaba.player.PlaybackController.play", "PlaybackController.kt", 43}}},
		{"a.b.c", "a", 1, []Symbol{{"com.hamzalabs.maktaba.player.PlaybackController.play", "PlaybackController.kt", 40}}},
		// line 2 is Queue.next inlined into play: the innermost first, its own class's file
		{"a.b.c", "a", 2, []Symbol{
			{"com.hamzalabs.maktaba.core.Queue.next", "Queue.kt", 88},
			{"com.hamzalabs.maktaba.player.PlaybackController.play", "PlaybackController.kt", 41},
		}},
		// two methods share b: the line tells them apart
		{"a.b.c", "b", 2, []Symbol{{"com.hamzalabs.maktaba.player.PlaybackController.stop", "PlaybackController.kt", 61}}},
		{"a.b.c", "b", 5, []Symbol{{"com.hamzalabs.maktaba.player.PlaybackController.stop", "PlaybackController.kt", 71}}},
		// no line: the method's names (one when they agree; a call inlined into it is not
		// one of them), no line
		{"a.b.c", "b", 0, []Symbol{{"com.hamzalabs.maktaba.player.PlaybackController.stop", "PlaybackController.kt", 0}}},
		{"a.b.c", "a", 0, []Symbol{{"com.hamzalabs.maktaba.player.PlaybackController.play", "PlaybackController.kt", 0}}},
		// two different methods under one name and no line: both, told apart by " | "
		{"a.b.f", "x", 0, []Symbol{{"com.hamzalabs.maktaba.player.Overloads.open | com.hamzalabs.maktaba.player.Overloads.close", "", 0}}},
		// a method with no range
		{"a.b.c", "c", 9, []Symbol{{"com.hamzalabs.maktaba.player.PlaybackController.reset", "PlaybackController.kt", 9}}},
		// a method the mapping does not rename, in a renamed class
		{"a.b.d", "toString", 3, []Symbol{{"com.hamzalabs.maktaba.core.Queue.toString", "Queue.kt", 0}}},
	}
	for _, c := range cases {
		got := tab.Retrace(c.class, c.method, c.line)
		if len(got) != len(c.want) {
			t.Errorf("%s.%s:%d = %+v", c.class, c.method, c.line, got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s.%s:%d [%d] = %+v, want %+v", c.class, c.method, c.line, i, got[i], c.want[i])
			}
		}
	}
	// a class the mapping does not rename (the platform's, a kept one) is left alone
	if got := tab.Retrace("android.os.Handler", "dispatchMessage", 106); got != nil {
		t.Errorf("a platform frame = %+v", got)
	}
	if name, ok := tab.RetraceClass("a.b.e"); !ok || name != "com.hamzalabs.maktaba.player.PlaybackError" {
		t.Errorf("class = %q %v", name, ok)
	}
}

func TestR8TableRoundTrip(t *testing.T) {
	tab := mapping(t)
	b, err := tab.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.Retrace("a.b.c", "a", 4); len(s) != 1 || s[0].Line != 43 {
		t.Fatalf("decoded retrace = %+v", s)
	}
	// a mapping's key must be its app's and release's: one can't stand in for another
	forged := *tab
	forged.r8 = nil
	forged.Release = "0.3.103"
	if _, err := Decode(gz(t, forged)); err == nil {
		t.Error("a mapping under another release's key decoded")
	}
	other := *tab
	other.r8 = nil
	other.Kind = "elf"
	if _, err := Decode(gz(t, other)); err == nil {
		t.Error("an unknown kind decoded")
	}
	empty := *tab
	empty.r8 = nil
	empty.Classes = nil
	if _, err := Decode(gz(t, empty)); err == nil {
		t.Error("a mapping with no classes decoded")
	}
}

// a Sentry event as the Android and desktop apps send one (KeptCrash.event): frames oldest
// first, "pkg.Class.method" with the file and line
func jvmEvent(typ string, frames ...[3]any) json.RawMessage {
	fs := []map[string]any{}
	for _, f := range frames {
		fs = append(fs, map[string]any{"function": f[0], "filename": f[1], "lineno": f[2],
			"in_app": strings.HasPrefix(f[0].(string), "a.") || strings.HasPrefix(f[0].(string), "com.hamzalabs")})
	}
	b, _ := json.Marshal(map[string]any{
		"platform": "java",
		"exception": map[string]any{"values": []any{map[string]any{"type": typ, "value": typ,
			"stacktrace": map[string]any{"frames": fs}}}},
	})
	return b
}

func TestSymbolicateAndroid(t *testing.T) {
	tab := mapping(t)
	ev := jvmEvent("a.b.e",
		[3]any{"android.os.Handler.dispatchMessage", "Handler.java", 106},
		[3]any{"a.b.c.a", "SourceFile", 2},
		[3]any{"a.b.c.b", "SourceFile", 5},
	)
	rel := Release{App: "android.player", Version: "0.3.102"}
	got := Symbolicate(context.Background(), []json.RawMessage{ev}, finder{tab.DebugID: tab}, rel)
	if len(got) != 1 {
		t.Fatalf("crashes = %d", len(got))
	}
	c := got[0]
	if c.Symbols != SymbolsMapping || c.Type != "com.hamzalabs.maktaba.player.PlaybackError" || c.Value != c.Type || len(c.Missing) != 0 {
		t.Fatalf("crash = %+v", c)
	}
	// top first: stop, then play's frame as two (Queue.next inlined into play), then the platform's
	if len(c.Frames) != 4 {
		t.Fatalf("frames = %+v", c.Frames)
	}
	if f := c.Frames[0]; f.Raw != "a.b.c.b(SourceFile:5)" || f.Symbol.Function != "com.hamzalabs.maktaba.player.PlaybackController.stop" || f.Symbol.Line != 71 || f.Inlined {
		t.Errorf("top = %+v %+v", f, f.Symbol)
	}
	if f := c.Frames[1]; f.Raw != "a.b.c.a(SourceFile:2)" || f.Symbol.Function != "com.hamzalabs.maktaba.core.Queue.next" || !f.Inlined || !f.InApp {
		t.Errorf("the inlined frame = %+v %+v", f, f.Symbol)
	}
	if f := c.Frames[2]; f.Raw != "" || f.Symbol.Function != "com.hamzalabs.maktaba.player.PlaybackController.play" || f.Symbol.Line != 41 || f.Inlined {
		t.Errorf("its caller = %+v %+v", f, f.Symbol)
	}
	if f := c.Frames[3]; f.Symbol != nil || f.Raw != "android.os.Handler.dispatchMessage(Handler.java:106)" {
		t.Errorf("a platform frame = %+v", f)
	}
	// another release's crash: its mapping is missing, the frames stay as they came
	got = Symbolicate(context.Background(), []json.RawMessage{ev}, finder{tab.DebugID: tab},
		Release{App: "android.player", Version: "0.3.103"})
	c = got[0]
	if c.Symbols != "" || len(c.Missing) != 1 || c.Missing[0] != (Image{CodeFile: "android.player", DebugID: R8ID("android.player", "0.3.103"),
		Kind: KindR8, Version: "0.3.103"}) || c.Frames[0].Symbol != nil || c.Type != "a.b.e" {
		t.Errorf("no mapping: %+v", c)
	}
	// a dSYM's table under the mapping's key is not read as one
	got = Symbolicate(context.Background(), []json.RawMessage{ev}, finder{tab.DebugID: &Table{DebugID: tab.DebugID}}, rel)
	if got[0].Symbols != "" || len(got[0].Missing) != 1 {
		t.Errorf("a dSYM table as a mapping: %+v", got[0])
	}
}

// The desktop player is not obfuscated (its installers are built without ProGuard): its
// frames are its code's names, read as they are, with nothing missing.
func TestSymbolicateDesktop(t *testing.T) {
	ev := jvmEvent("java.lang.IllegalStateException",
		[3]any{"java.lang.Thread.run", "Thread.java", 1583},
		[3]any{"com.hamzalabs.maktaba.desktop.player.Player.open", "Player.kt", 88},
		[3]any{"com.hamzalabs.maktaba.desktop.player.Player.fail", "", 0},
	)
	got := Symbolicate(context.Background(), []json.RawMessage{ev}, finder{}, Release{App: "desktop.player", Version: "0.3.102"})
	if len(got) != 1 {
		t.Fatalf("crashes = %d", len(got))
	}
	c := got[0]
	if c.Symbols != SymbolsSource || len(c.Missing) != 0 || c.Type != "java.lang.IllegalStateException" || len(c.Frames) != 3 {
		t.Fatalf("crash = %+v", c)
	}
	if f := c.Frames[0]; f.Raw != "com.hamzalabs.maktaba.desktop.player.Player.fail" || !f.InApp || f.Symbol != nil {
		t.Errorf("a frame with no line = %+v", f)
	}
	if f := c.Frames[1]; f.Raw != "com.hamzalabs.maktaba.desktop.player.Player.open(Player.kt:88)" {
		t.Errorf("a frame with its line = %+v", f)
	}
	// an Apple crash in the same report is still read by its UUIDs, not as a JVM one
	apple := crashEvent([][2]any{{"crashy", 0x10}}, map[string]string{"crashy": "11111111-2222-3333-4444-555555555555"})
	got = Symbolicate(context.Background(), []json.RawMessage{apple}, finder{}, Release{App: "desktop.player"})
	if got[0].Symbols != "" || len(got[0].Missing) != 1 || got[0].Missing[0].Kind != "" {
		t.Errorf("an Apple crash: %+v", got[0])
	}
}
