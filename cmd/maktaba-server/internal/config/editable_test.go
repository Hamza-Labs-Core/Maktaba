package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func readRaw(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if _, err := toml.DecodeFile(path, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A change from the settings page keeps every other key of the file: those Config
// knows, those it does not (a newer release's), and [database] url unset.
func TestSettingsSaveKeepsEveryOtherKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "server.toml")
	body := `[server]
listen = "0.0.0.0:8080"
data_dir = "/srv/maktaba"
future_knob = "kept"

[media]
path_map = "/Volumes/O/=/old/"
ffmpeg = "/opt/ffmpeg"

[postgres]
port = 54329

[tomorrow]
answer = 42
nested = { a = "b" }
`
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	before := readRaw(t, path)
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	s := EditableOf(cfg)
	s.Media.PathMap = []PathPair{{DB: "/Volumes/O/", Local: "/new/"}, {DB: "Y:/", Local: "/y/"}}
	s.Server.MDNS = false
	s.Into(&cfg)
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	after := readRaw(t, path)
	if _, ok := after["database"]; ok {
		t.Errorf("[database] written though the file never set it: %v", after["database"])
	}
	if got := after["media"].(map[string]any)["path_map"]; got != "/Volumes/O/=/new/;Y:/=/y/" {
		t.Errorf("path_map = %v", got)
	}
	if got := after["server"].(map[string]any)["mdns"]; got != "off" {
		t.Errorf("mdns = %v, want off", got)
	}
	if !reflect.DeepEqual(after["tomorrow"], before["tomorrow"]) {
		t.Errorf("unknown table: %v, want %v", after["tomorrow"], before["tomorrow"])
	}
	for table, keys := range map[string][]string{
		"server":   {"listen", "data_dir", "future_knob"},
		"media":    {"ffmpeg"},
		"postgres": {"port"},
	} {
		for _, k := range keys {
			b := before[table].(map[string]any)[k]
			a := after[table].(map[string]any)[k]
			if !reflect.DeepEqual(a, b) {
				t.Errorf("%s.%s = %v, want %v", table, k, a, b)
			}
		}
	}
	// POSIX modes: Windows keeps the file the user's by its ACL (files_windows.go)
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}

	// a url the file sets stays set
	if err := writeFile(path, "[database]\nurl = \"postgres://h/db\"\n"); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ = LoadFrom(path)
	s = EditableOf(cfg)
	s.Server.Listen = "0.0.0.0:9999"
	s.Into(&cfg)
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, path)["database"].(map[string]any)["url"]; got != "postgres://h/db" {
		t.Errorf("database url = %v", got)
	}
}

func TestPathPairs(t *testing.T) {
	t.Parallel()
	got := ParsePathPairs(" /a/=/x/ ; bad ; =/y; Y:/=/Volumes/Y ")
	want := []PathPair{{"/a/", "/x/"}, {"Y:/", "/Volumes/Y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %v", got)
	}
	if FormatPathPairs(want) != "/a/=/x/;Y:/=/Volumes/Y" {
		t.Fatal(FormatPathPairs(want))
	}
	if got := ParsePathPairs(`{"/b":"/y","/a":"/x"}`); !reflect.DeepEqual(got, []PathPair{{"/a", "/x"}, {"/b", "/y"}}) {
		t.Fatalf("json = %v", got)
	}
	if len(ParsePathPairs("")) != 0 || ParsePathPairs("") == nil {
		t.Fatal("an empty map is an empty list")
	}
	pairs := []PathPair{{"/Volumes/O", "/mnt/o"}, {"/Volumes/O/Deep/", "/mnt/deep"}}
	for in, out := range map[string]string{
		"/volumes/o/Talks":     filepath.FromSlash("/mnt/o/Talks"),
		"/Volumes/O/Deep/a":    filepath.FromSlash("/mnt/deep/a"),
		"/Volumes/O":           filepath.FromSlash("/mnt/o/"),
		"/Volumes/Other/x":     "/Volumes/Other/x",
		`\Volumes\O\win\style`: filepath.FromSlash("/mnt/o/win/style"),
	} {
		if got := MapToLocal(pairs, in); got != out {
			t.Errorf("MapToLocal(%q) = %q, want %q", in, got, out)
		}
	}
}

func TestApplyNamesChangedKeys(t *testing.T) {
	t.Parallel()
	s := Editable{Media: EditableMedia{PathMap: []PathPair{}, Roots: []string{"/a"}},
		Server: EditableServer{MDNS: true, Listen: ":8080", WebListen: ":8088"}}
	var p SettingsPatch
	listen, mdns, roots := " :8080 ", false, []string{"/a", "/b"}
	p.Server = &struct {
		MDNS         *bool   `json:"mdns"`
		Listen       *string `json:"listen"`
		WebListen    *string `json:"web_listen"`
		StreamAddr   *string `json:"stream_addr"`
		PublicURL    *string `json:"public_url"`
		WebPublicURL *string `json:"web_public_url"`
	}{MDNS: &mdns, Listen: &listen}
	p.Media = &struct {
		PathMap *[]PathPair `json:"path_map"`
		Roots   *[]string   `json:"roots"`
	}{Roots: &roots}
	changed := p.Apply(&s)
	if !reflect.DeepEqual(changed, []string{"media.roots", "server.mdns"}) {
		t.Fatalf("changed = %v", changed)
	}
	if s.Server.Listen != ":8080" || s.Server.WebListen != ":8088" || s.Server.MDNS || len(s.Media.Roots) != 2 {
		t.Fatalf("applied = %+v", s)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "share", "Talks"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := Editable{
		Media: EditableMedia{PathMap: []PathPair{}, Roots: []string{}},
		Server: EditableServer{MDNS: true, Listen: "0.0.0.0:8080", WebListen: "0.0.0.0:8088",
			StreamAddr: ":8081"},
	}
	busy := Checks{PortFree: func(addr string) error {
		if strings.HasSuffix(addr, ":7000") {
			return errors.New("in use")
		}
		return nil
	}}
	cases := []struct {
		name   string
		change func(*Editable)
		fields []string
	}{
		{"unchanged", func(*Editable) {}, nil},
		{"free port", func(s *Editable) { s.Server.Listen = "127.0.0.1:7001" }, nil},
		{"localhost", func(s *Editable) { s.Server.WebListen = "localhost:7002" }, nil},
		{"ipv6", func(s *Editable) { s.Server.StreamAddr = "[::1]:7003" }, nil},
		{"port in use", func(s *Editable) { s.Server.Listen = ":7000" }, []string{"server.listen"}},
		{"no port", func(s *Editable) { s.Server.Listen = "0.0.0.0" }, []string{"server.listen"}},
		{"port 0", func(s *Editable) { s.Server.Listen = ":0" }, []string{"server.listen"}},
		{"port too big", func(s *Editable) { s.Server.Listen = ":70000" }, []string{"server.listen"}},
		{"host name", func(s *Editable) { s.Server.WebListen = "nas.local:8088" }, []string{"server.web_listen"}},
		{"empty", func(s *Editable) { s.Server.StreamAddr = "" }, []string{"server.stream_addr"}},
		{"listen = web_listen", func(s *Editable) { s.Server.WebListen = ":8080" },
			[]string{"server.web_listen", "server.listen"}},
		{"public url", func(s *Editable) {
			s.Server.PublicURL = "http://192.168.1.20:8081"
			s.Server.WebPublicURL = "https://maktaba.example/"
		}, nil},
		{"public url path", func(s *Editable) { s.Server.PublicURL = "http://h:8081/x" }, []string{"server.public_url"}},
		{"public url query", func(s *Editable) { s.Server.PublicURL = "http://h?x=1" }, []string{"server.public_url"}},
		{"public url creds", func(s *Editable) { s.Server.WebPublicURL = "http://u:p@h" }, []string{"server.web_public_url"}},
		{"public url scheme", func(s *Editable) { s.Server.WebPublicURL = "ftp://h" }, []string{"server.web_public_url"}},
		{"public url bare", func(s *Editable) { s.Server.WebPublicURL = "h:8088" }, []string{"server.web_public_url"}},
		{"path map", func(s *Editable) {
			s.Media.PathMap = []PathPair{{"/Volumes/O/", filepath.Join(dir, "share")}, {"O:/", filepath.Join(dir, "share")}}
		}, nil},
		{"path map relative db", func(s *Editable) { s.Media.PathMap = []PathPair{{"Volumes/O", dir}} },
			[]string{"media.path_map[0].db"}},
		{"path map relative local", func(s *Editable) { s.Media.PathMap = []PathPair{{"/O", "share"}} },
			[]string{"media.path_map[0].local"}},
		{"path map missing local", func(s *Editable) { s.Media.PathMap = []PathPair{{"/O", filepath.Join(dir, "nope")}} },
			[]string{"media.path_map[0].local"}},
		{"path map local a file", func(s *Editable) { s.Media.PathMap = []PathPair{{"/O", file}} },
			[]string{"media.path_map[0].local"}},
		{"path map separator", func(s *Editable) { s.Media.PathMap = []PathPair{{"/O;x", dir}} },
			[]string{"media.path_map[0]"}},
		{"path map twice", func(s *Editable) { s.Media.PathMap = []PathPair{{"/O/", dir}, {"/o", dir}} },
			[]string{"media.path_map[1].db"}},
		{"root through the map", func(s *Editable) {
			s.Media.PathMap = []PathPair{{"/Volumes/O/", filepath.Join(dir, "share")}}
			s.Media.Roots = []string{"/Volumes/O/Talks", dir}
		}, nil},
		{"root not mapped", func(s *Editable) { s.Media.Roots = []string{"/Volumes/Nowhere/Talks"} },
			[]string{"media.roots[0]"}},
		{"root relative", func(s *Editable) { s.Media.Roots = []string{"Talks"} }, []string{"media.roots[0]"}},
		{"root a file", func(s *Editable) { s.Media.Roots = []string{file} }, []string{"media.roots[0]"}},
	}
	for _, c := range cases {
		next := base
		c.change(&next)
		var got []string
		for _, e := range busy.Validate(next, base, base) {
			if e.Message == "" {
				t.Errorf("%s: %s has no message", c.name, e.Field)
			}
			got = append(got, e.Field)
		}
		if !reflect.DeepEqual(got, c.fields) {
			t.Errorf("%s: fields %v, want %v", c.name, got, c.fields)
		}
	}
}

// The address serve holds now is not free, and is no change: a write that puts the
// running one back is not refused.
func TestValidateRunningAddressIsNotProbed(t *testing.T) {
	t.Parallel()
	saved := Editable{Server: EditableServer{Listen: ":7100", WebListen: ":8088", StreamAddr: ":8081"}}
	running := saved
	running.Server.Listen = ":8080"
	next := saved
	next.Server.Listen = ":8080"
	probed := false
	errs := Checks{PortFree: func(string) error { probed = true; return errors.New("busy") }}.Validate(next, saved, running)
	if len(errs) != 0 || probed {
		t.Fatalf("errs %v probed %v", errs, probed)
	}
}

func TestOnlyChanged(t *testing.T) {
	t.Parallel()
	errs := []FieldError{{Field: "media.roots[0]"}, {Field: "server.listen"}, {Field: "media.path_map[1].local"}}
	if got := OnlyChanged(errs, []string{"server.mdns"}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := OnlyChanged(errs, []string{"media.path_map"}); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := OnlyChanged(errs, []string{"server.listen"}); len(got) != 1 || got[0].Field != "server.listen" {
		t.Fatalf("%v", got)
	}
}
