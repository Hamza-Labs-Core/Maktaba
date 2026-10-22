package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// encodings a node.json can be saved in by an editor (Notepad, PowerShell 5.1's
// Set-Content -Encoding UTF8, Out-File and >): every reader reads them all.
func encoded(t *testing.T, text, enc string) []byte {
	t.Helper()
	switch enc {
	case "utf-8":
		return []byte(text)
	case "utf-8 BOM":
		return append([]byte("\xef\xbb\xbf"), text...)
	case "utf-16le", "utf-16be":
		var out []byte
		if enc == "utf-16le" {
			out = []byte{0xff, 0xfe}
		} else {
			out = []byte{0xfe, 0xff}
		}
		for _, u := range utf16.Encode([]rune(text)) {
			if enc == "utf-16le" {
				out = append(out, byte(u), byte(u>>8))
			} else {
				out = append(out, byte(u>>8), byte(u))
			}
		}
		return out
	case "utf-32le":
		out := []byte{0xff, 0xfe, 0, 0}
		for _, r := range text {
			out = append(out, byte(r), byte(r>>8), byte(r>>16), byte(r>>24))
		}
		return out
	}
	t.Fatalf("no encoding %s", enc)
	return nil
}

func readFields(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // the test's temp file
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || b[0] != '{' {
		t.Fatalf("%s is not plain UTF-8 JSON: % x", path, b[:min(len(b), 4)])
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func addCA(fields map[string]json.RawMessage) error {
	fields["database_ca"] = json.RawMessage(`"PEM"`)
	return nil
}

// maktaba-server's write of node.json (a switch, a standby, the home's database CA) keeps
// the fields only the pipeline writes (joined_at, machine_id) whatever encoding an editor
// saved the file in, and writes it back as plain UTF-8.
func TestWriteNodeConfigKeepsThePipelinesFieldsInAnyEncoding(t *testing.T) {
	const text = `{"node_id":"n1","private_key":"k","database_url":"postgres://h","joined_at":"2026-01-01T00:00:00Z","machine_id":"pc-a","name":"Ḥamza"}`
	for _, enc := range []string{"utf-8", "utf-8 BOM", "utf-16le", "utf-16be", "utf-32le"} {
		t.Run(enc, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Maktaba", "node.json")
			writeAt(t, path, string(encoded(t, text, enc)))
			if err := WriteNodeConfig(path, addCA); err != nil {
				t.Fatal(err)
			}
			got := readFields(t, path)
			if got["joined_at"] != "2026-01-01T00:00:00Z" || got["machine_id"] != "pc-a" || got["database_ca"] != "PEM" ||
				got["node_id"] != "n1" || got["name"] != "Ḥamza" {
				t.Fatalf("after the write: %v", got)
			}
			if names := dirNames(t, filepath.Dir(path)); strings.Join(names, ",") != "node.json" {
				t.Fatalf("left beside it: %v", names)
			}
		})
	}
}

// The fields are read from the file that holds the node config (a write that could not
// finish left node.json under its backup name), and a node.json that is there but cannot
// be read is never written over with less.
func TestWriteNodeConfigReadsTheFileThatHoldsIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Maktaba")
	path := filepath.Join(dir, "node.json")
	writeAt(t, filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak"),
		`{"node_id":"n1","private_key":"k","database_url":"postgres://h","joined_at":"2026-01-01T00:00:00Z"}`)
	if err := WriteNodeConfig(path, addCA); err != nil {
		t.Fatal(err)
	}
	if got := readFields(t, path); got["joined_at"] != "2026-01-01T00:00:00Z" || got["database_ca"] != "PEM" {
		t.Fatalf("from the backup: %v", got)
	}

	unreadable := filepath.Join(t.TempDir(), "node.json")
	if err := os.MkdirAll(unreadable, 0o700); err != nil { // a node config that cannot be read
		t.Fatal(err)
	}
	if err := WriteNodeConfig(unreadable, addCA); err == nil || !strings.Contains(err.Error(), "node config") {
		t.Fatalf("a node config that cannot be read: %v", err)
	}
}

// A home's switch moves its node.json aside (the file that holds it) and what a write
// that could not finish left of it goes too: a reader would take that as the node config
// again on a machine that is the home now.
func TestSetNodeConfigAsideTakesItsLeftovers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Maktaba")
	path, aside := filepath.Join(dir, "node.json"), filepath.Join(dir, "node.json.was-node-2")
	writeAt(t, path, `{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`)
	writeAt(t, filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak"), `{"node_id":"n0","private_key":"k"}`)
	writeAt(t, filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.tmp"), `{"node_id":"n0","private_key":"k"}`)
	if err := SetNodeConfigAside(path, aside); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, dir); strings.Join(names, ",") != "node.json.was-node-2" {
		t.Fatalf("after: %v", names)
	}
	if got := readFields(t, aside); got["node_id"] != "n1" {
		t.Fatalf("moved aside: %v", got)
	}
	if err := SetNodeConfigAside(path, aside); err != nil { // already moved: no error
		t.Fatal(err)
	}

	// node.json gone, its backup whole: the backup is what is moved
	writeAt(t, filepath.Join(dir, ".node.json-1792600001-4242-0badf00d.bak"), `{"node_id":"n2","private_key":"k"}`)
	if err := SetNodeConfigAside(path, filepath.Join(dir, "node.json.was-node-3")); err != nil {
		t.Fatal(err)
	}
	if got := readFields(t, filepath.Join(dir, "node.json.was-node-3")); got["node_id"] != "n2" {
		t.Fatalf("the backup moved aside: %v", got)
	}
}

// A UTF-16 or UTF-32 node.json that does not decode is broken, as for the pipeline.
func TestWithoutBOMRefusesWhatDoesNotDecode(t *testing.T) {
	for name, b := range map[string][]byte{
		"odd UTF-16":          []byte("\xff\xfe{\x00\""),
		"a lone surrogate":    []byte("\xff\xfe\x00\xd8{\x00"),
		"UTF-32 out of range": []byte("\xff\xfe\x00\x00\x00\x00\x11\x00"),
	} {
		if got := withoutBOM(b); got != nil {
			t.Errorf("%s: %q, want none", name, got)
		}
	}
	if got := string(withoutBOM(encoded(t, `{"a":"é"}`, "utf-16be"))); got != `{"a":"é"}` {
		t.Errorf("UTF-16BE: %q", got)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
