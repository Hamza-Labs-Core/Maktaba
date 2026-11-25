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
