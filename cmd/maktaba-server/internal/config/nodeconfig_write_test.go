package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// Text that does not decode strictly is broken, as for the pipeline and PowerShell: a
// cut or lone-surrogate UTF-16 file, UTF-32 out of range, an ANSI file with a byte that
// is not UTF-8 (PowerShell 5.1's Set-Content with a name like Hélène).
func TestWithoutBOMRefusesWhatDoesNotDecode(t *testing.T) {
	for name, b := range map[string][]byte{
		"odd UTF-16":          []byte("\xff\xfe{\x00\""),
		"a lone surrogate":    []byte("\xff\xfe\x00\xd8{\x00"),
		"UTF-32 out of range": []byte("\xff\xfe\x00\x00\x00\x00\x11\x00"),
		"ANSI":                []byte("{\"name\":\"H\xe9l\xe8ne\"}"),
		"ANSI after a BOM":    []byte("\xef\xbb\xbf{\"name\":\"H\xe9l\xe8ne\"}"),
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

// While maktaba-server writes node.json over and over (ReplaceFileW renames it aside for
// a moment on Windows), a reader waits every write out: in many reads, none is "not
// joined" (which at serve's start means starting as the home) and none is an error.
func TestReadNodeConfigWaitsOutWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Maktaba")
	path := filepath.Join(dir, "node.json")
	writeAt(t, path, `{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`)
	getenv := func(k string) string {
		if k == "MAKTABA_NODE_CONFIG" {
			return path
		}
		return ""
	}
	stop := make(chan struct{})
	done := make(chan int)
	go func() {
		writes := 0
		for {
			select {
			case <-stop:
				done <- writes
				return
			default:
			}
			if WriteNodeConfig(path, addCA) == nil {
				writes++
			}
		}
	}()
	end := time.Now().Add(time.Second)
	reads, notJoined, failed := 0, 0, 0
	for time.Now().Before(end) {
		reads++
		switch n := ReadNodeConfig(getenv, "windows", "", os.ReadFile); {
		case n.Err != nil:
			failed++
			t.Logf("read %d: %v", reads, n.Err)
		case !n.Joined():
			notJoined++
		}
	}
	close(stop)
	writes := <-done
	if notJoined != 0 || failed != 0 || writes == 0 {
		t.Fatalf("%d reads during %d writes: %d not joined, %d errors", reads, writes, notJoined, failed)
	}
}

// A field update changes only the fields it is about, on node.json as it is when the
// lock is held: every other field stays as the file has it, and a node config that is
// another node's now (a re-join between the read and the write) is left as it is.
func TestUpdateNodeConfigChangesOnlyItsFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Maktaba")
	path := filepath.Join(dir, "node.json")
	writeAt(t, path, `{"node_id":"n1","private_key":"k","database_url":"postgres://h","joined_at":"then",`+
		`"home":{"url":"http://home:8080","term":2},"later":{"kept":true},"peers":["http://p:8080"]}`)
	err := UpdateNodeConfig(path, "n1", func(n *NodeConfig) {
		n.DatabaseCA = "PEM"
		n.Peers = nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFields(t, path)
	if got["database_ca"] != "PEM" || got["joined_at"] != "then" || got["later"] == nil || got["peers"] != nil {
		t.Fatalf("after the update: %v", got)
	}
	if home, _ := got["home"].(map[string]any); home["url"] != "http://home:8080" || home["term"] != float64(2) {
		t.Fatalf("home: %v", got["home"])
	}

	rejoined := `{"node_id":"n2","private_key":"k2","database_url":"postgres://h2"}`
	writeAt(t, path, rejoined)
	err = UpdateNodeConfig(path, "n1", func(n *NodeConfig) { n.DatabaseCA = "OLD" })
	if err == nil || !strings.Contains(err.Error(), `"n2"`) {
		t.Fatalf("a node config re-joined meanwhile: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != rejoined { //nolint:gosec // the test's temp file
		t.Fatalf("the re-joined node config was written: %s", b)
	}
}

// Set aside with node.json broken and the config in a write's leftover: the broken file
// is kept aside (never read as a node config), the leftover is what is moved, and no
// whole node config is left to read after it.
func TestSetNodeConfigAsideWithABrokenNodeJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Maktaba")
	path, aside := filepath.Join(dir, "node.json"), filepath.Join(dir, "node.json.was-node-1")
	writeAt(t, path, `{"node_id":"n1","priv`)
	writeAt(t, filepath.Join(dir, ".node.json-1792600000-4242-0badf00d.bak"), `{"node_id":"n1","private_key":"k"}`)
	writeAt(t, filepath.Join(dir, ".node.json-1792600000-4242-0badf00e.tmp"), `{"node_id":"n0","private_key":"k"}`)
	if err := SetNodeConfigAside(path, aside); err != nil {
		t.Fatal(err)
	}
	if got := readFields(t, aside); got["node_id"] != "n1" {
		t.Fatalf("moved aside: %v", got)
	}
	names := dirNames(t, dir)
	if len(names) != 2 || names[0] != "node.json.broken-"+names[0][len("node.json.broken-"):] || names[1] != "node.json.was-node-1" {
		t.Fatalf("after: %v", names)
	}
	if held := nodeConfigFile(path, os.ReadFile); isFile(held) {
		t.Fatalf("%s is still read as the node config", held)
	}
}

// While a write replaces the roaming node.json of a machine that joined before Epic 38
// (its lock taken, node.json under its backup name for a moment, as ReplaceFileW does),
// the path resolves to it every time: never to %LOCALAPPDATA%, where nothing is joined.
func TestNodeConfigPathWaitsOutAWrite(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{
		"APPDATA":            filepath.Join(root, "Roaming"),
		"LOCALAPPDATA":       filepath.Join(root, "Local"),
		"MAKTABA_MACHINE_ID": "pc-a",
	}
	getenv := func(k string) string { return env[k] }
	dir := filepath.Join(root, "Roaming", "Maktaba")
	path := filepath.Join(dir, "node.json")
	body := `{"node_id":"n1","private_key":"k","database_url":"postgres://h","machine_id":"pc-a"}`
	writeAt(t, path, body)
	stop := make(chan struct{})
	done := make(chan int)
	go func() { // the writer: lock, node.json aside, the new one in, the backup gone
		writes := 0
		lock := nodeConfigLockPath(path)
		for {
			select {
			case <-stop:
				done <- writes
				return
			default:
			}
			_ = os.WriteFile(lock, nil, 0o600)
			tmp, bak := nodeConfigLeftoverName(path, ".tmp"), nodeConfigLeftoverName(path, ".bak")
			_ = os.WriteFile(tmp, []byte(body), 0o600)
			_ = os.Rename(path, bak)
			time.Sleep(200 * time.Microsecond) // the gap
			_ = os.Rename(tmp, path)
			_ = os.Remove(bak)
			_ = os.Remove(lock)
			writes++
		}
	}()
	end := time.Now().Add(time.Second)
	reads, elsewhere := 0, 0
	for time.Now().Before(end) {
		reads++
		if got := NodeConfigPath(getenv, "windows", root); got != path {
			elsewhere++
			t.Logf("read %d: %s", reads, got)
		}
	}
	close(stop)
	if writes := <-done; elsewhere != 0 || writes == 0 {
		t.Fatalf("%d of %d resolutions during %d writes were not the roaming node config", elsewhere, reads, writes)
	}
}
