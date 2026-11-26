package pgbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A dump's chunks, whole or not, by what is in its folder alone.
func TestCheckChunks(t *testing.T) {
	const sig = "PGCOPY\n\xff\r\n\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	const row = "\x00\x01\x00\x00\x00\x02hi"
	const end = "\xff\xff"
	toc := func(descs ...string) string {
		var sb strings.Builder
		for i, d := range descs {
			sb.WriteString(string(rune('1'+i)) + "; 0 1 " + d + " public segment_embeddings maktaba\n")
		}
		return sb.String()
	}
	for name, c := range map[string]struct {
		files map[string]string // by number
		toc   string
		ok    bool
	}{
		"whole":                     {map[string]string{"000": sig + row + end, "001": sig + end}, toc("TABLE"), true},
		"none, pg_dump's own":       {nil, toc("TABLE", "TABLE DATA"), true},
		"none, and no such table":   {nil, "", true},
		"none of its rows":          {nil, toc("TABLE"), false},
		"cut between rows":          {map[string]string{"000": sig + row}, toc("TABLE"), false},
		"cut in a row":              {map[string]string{"000": sig + row[:5]}, toc("TABLE"), false},
		"cut in its trailer":        {map[string]string{"000": sig + row + end[:1]}, toc("TABLE"), false},
		"empty":                     {map[string]string{"000": ""}, toc("TABLE"), false},
		"the header alone":          {map[string]string{"000": sig}, toc("TABLE"), false},
		"another file":              {map[string]string{"000": strings.Repeat("x", 40) + end}, toc("TABLE"), false},
		"the first gone":            {map[string]string{"001": sig + end}, toc("TABLE"), false},
		"one gone from the middle":  {map[string]string{"000": sig + end, "002": sig + end}, toc("TABLE"), false},
		"one's number is not whole": {map[string]string{"000": sig + end, "1": sig + end}, toc("TABLE"), false},
	} {
		dir := t.TempDir()
		for n, body := range c.files {
			if err := os.WriteFile(filepath.Join(dir, "chunk.public.segment_embeddings."+n+".bin"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := CheckChunks(dir, c.toc); (err == nil) != c.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The counts of every table whose rows the dump holds, and of no other.
func TestCountsScript(t *testing.T) {
	dir := t.TempDir()
	list := "1; 0 1 TABLE public a maktaba\n2; 0 2 TABLE DATA public a maktaba\n3; 0 3 TABLE DATA public o'b maktaba\n"
	if sql, err := CountsScript(dir, list); err != nil || sql != "" {
		t.Fatalf("no counts: %q %v", sql, err)
	}
	if err := os.WriteFile(filepath.Join(dir, CountsFile), []byte(`{"public.a":3,"public.o'b":0,"public.gone":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sql, err := CountsScript(dir, list)
	if err != nil || strings.Count(sql, "DO $mk$") != 2 || !strings.Contains(sql, `FROM "public"."a";`) || !strings.Contains(sql, "n <> 3") ||
		!strings.Contains(sql, "'public.o''b'") || strings.Contains(sql, "gone") {
		t.Fatalf("%v\n%s", err, sql)
	}
	if err := os.WriteFile(filepath.Join(dir, CountsFile), []byte(`{"public`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CountsScript(dir, list); err == nil {
		t.Fatal("damaged counts")
	}
}

// A chunked dump with no counts.json (a dump from v0.3.37 to v0.3.42) cannot tell a last
// chunk gone: the numbers the folder holds stay whole, and nothing else says the rows. That
// is the limit CheckChunks documents ("a last one gone is seen by the rows' counts alone"):
// pinned here, so a change that quietly takes such a dump for a whole one is noticed.
func TestCheckChunksCannotSeeAGoneLastChunkWithoutCounts(t *testing.T) {
	const sig = "PGCOPY\n\xff\r\n\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	const row = "\x00\x01\x00\x00\x00\x02hi"
	const end = "\xff\xff"
	toc := "1; 0 1 TABLE public segment_embeddings maktaba\n2; 0 2 TABLE DATA public segment_embeddings maktaba\n"
	dir := t.TempDir()
	// a dump taken with two chunks, its last one gone since: what is left looks whole
	for n, body := range map[string]string{"000": sig + row + end, "001": sig + row + end} {
		if err := os.WriteFile(filepath.Join(dir, "chunk.public.segment_embeddings."+n+".bin"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Remove(filepath.Join(dir, "chunk.public.segment_embeddings.001.bin"))
	if err := CheckChunks(dir, toc); err != nil {
		t.Fatalf("a last chunk gone, with no counts to see it: %v", err)
	}
	// with the counts the dump's snapshot wrote, the rows it lacks say it
	if err := os.WriteFile(filepath.Join(dir, CountsFile), []byte(`{"public.segment_embeddings":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if sql, err := CountsScript(dir, toc); err != nil || !strings.Contains(sql, "n <> 4") {
		t.Fatalf("the counts the dump has: %q %v", sql, err)
	}
}
