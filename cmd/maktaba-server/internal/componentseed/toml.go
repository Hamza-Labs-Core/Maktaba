package componentseed

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Key is a server.toml key that became desired state (story 51.03 §3): imported once by
// the seed, then only compared. [transcription] workers and [pipeline] index_worker are
// not here: they size and place the work of an enabled component, they turn nothing on.
type Key struct {
	Section, Name string
	Component     string
	// imported is the value the seed records (component_meta.toml_imported), as the code
	// reads the key today (an absent key: its default)
	imported func(config.Config) string
	// says is the want the file's value asks of Component, with the machine's facts; ok
	// false when the value asks nothing ([llm] enabled = "auto", an empty [llm] url,
	// [cloud] enabled with nothing to link with)
	says func(Facts) (want string, ok bool)
}

// byConfig is a says that reads only the file.
func byConfig(fn func(config.Config) (string, bool)) func(Facts) (string, bool) {
	return func(f Facts) (string, bool) { return fn(f.Config) }
}

// Name is "section.key".
func (k Key) String() string { return k.Section + "." + k.Name }

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// Keys are the imported keys, in the order they are reported.
var Keys = []Key{
	{"tv", "slots", "livetv.slots",
		func(c config.Config) string { return onOff(c.TV.SlotsOn()) },
		byConfig(func(c config.Config) (string, bool) { return want(c.TV.SlotsOn()), true })},
	{"llm", "enabled", "llm",
		func(c config.Config) string {
			if m := strings.ToLower(strings.TrimSpace(c.LLM.Enabled)); m != "" {
				return m
			}
			return "auto"
		},
		byConfig(func(c config.Config) (string, bool) {
			switch strings.ToLower(strings.TrimSpace(c.LLM.Enabled)) {
			case "off":
				return components.WantInstalled, true
			case "on":
				return components.WantEnabled, true
			}
			return "", false
		})},
	// whether a url is set, never the url (it may carry a login)
	{"llm", "url", "llm",
		func(c config.Config) string {
			if strings.TrimSpace(c.LLM.URL) != "" {
				return "set"
			}
			return "unset"
		},
		byConfig(func(c config.Config) (string, bool) {
			if strings.TrimSpace(c.LLM.URL) != "" {
				return components.WantEnabled, true
			}
			return "", false
		})},
	// enabled = true runs the relay only with something to link with (a credential, or a
	// claim code and slug) on a home (supervisor.RelayWanted, the seed's rule): without
	// it the key asks nothing, as the seed wrote cloud.link installed
	{"cloud", "enabled", "cloud.link",
		func(c config.Config) string { return strconv.FormatBool(c.Cloud.Enabled) },
		func(f Facts) (string, bool) {
			if !f.Config.Cloud.Enabled {
				return components.WantInstalled, true
			}
			if !f.Relay {
				return "", false
			}
			return components.WantEnabled, true
		}},
	{"server", "mdns", "discovery.mdns",
		func(c config.Config) string { return onOff(c.Server.MDNSOn()) },
		byConfig(func(c config.Config) (string, bool) { return want(c.Server.MDNSOn()), true })},
	{"backups", "nightly", "backups",
		func(c config.Config) string { return onOff(c.Backups.NightlyOn()) },
		byConfig(func(c config.Config) (string, bool) { return want(c.Backups.NightlyOn()), true })},
	{"telemetry", "enabled", "telemetry",
		func(c config.Config) string { return onOff(c.Telemetry.On()) },
		byConfig(func(c config.Config) (string, bool) { return want(c.Telemetry.On()), true })},
}

// Imported is what the seed records of the imported keys (component_meta.toml_imported).
func Imported(cfg config.Config) map[string]string {
	out := map[string]string{}
	for _, k := range Keys {
		out[k.String()] = k.imported(cfg)
	}
	return out
}

// Line is where a key is set in a server.toml: its 1-based line and its value as written.
type Line struct {
	N     int
	Value string
}

// Lines finds each of keys set in raw (a [section] table's key, or a top-level dotted
// "section.key"). A key the file does not set is not in the answer.
func Lines(raw []byte, keys []Key) map[string]Line {
	want := map[string]bool{}
	for _, k := range keys {
		want[k.String()] = true
	}
	out := map[string]Line{}
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			h := strings.TrimSpace(strings.Trim(stripComment(line), "[]"))
			section = strings.Trim(h, `"`)
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name := strings.Trim(strings.TrimSpace(k), `"`)
		full := name
		if section != "" {
			full = section + "." + name
		}
		if want[full] {
			if _, seen := out[full]; !seen {
				out[full] = Line{N: n, Value: unquote(stripComment(strings.TrimSpace(v)))}
			}
		}
	}
	return out
}

// stripComment drops a trailing # comment outside a quoted string.
func stripComment(s string) string {
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// Finding is a server.toml key whose value asks for a want other than the database's.
type Finding struct {
	Key       string // "tv.slots"
	Value     string // as written in the file
	Line      int
	Component string
	Says      string // the want the file's value asks for
	Desired   string // the want the database has (it wins)
}

// secretKeys are written in a finding as "set", never as their value: a url may carry a
// login, and a finding goes to the log and to a status row.
var secretKeys = map[string]bool{"llm.url": true}

// Superseded compares each imported key the file sets with its component's desired
// state. desired is the home's (store.Desired); a component with no row is not compared.
// f is the machine's facts at this boot (what [cloud] enabled can run).
func Superseded(raw []byte, f Facts, desired map[string]store.Desired) []Finding {
	lines := Lines(raw, Keys)
	var out []Finding
	for _, k := range Keys {
		l, set := lines[k.String()]
		if !set {
			continue
		}
		says, ok := k.says(f)
		d, have := desired[k.Component]
		if !ok || !have || d.Want == says {
			continue
		}
		value := l.Value
		if secretKeys[k.String()] {
			value = "set"
		}
		out = append(out, Finding{Key: k.String(), Value: value, Line: l.N, Component: k.Component,
			Says: says, Desired: d.Want})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// LogSuperseded writes one toml_key_superseded line per finding (a boot calls it once).
func LogSuperseded(w io.Writer, path string, found []Finding) {
	for _, f := range found {
		fmt.Fprintf(w, "[serve] level=warn event=toml_key_superseded key=%s value=%q line=%d file=%q component=%s "+
			"file_says=%s desired=%s: server.toml says otherwise; the setting in the database wins\n",
			f.Key, f.Value, f.Line, path, f.Component, f.Says, f.Desired)
	}
}
