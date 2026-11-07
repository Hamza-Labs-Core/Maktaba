// Package pluginmanifest reads and checks a Maktaba plugin's manifest.toml (Epic 54,
// story 54.01): the schema in shared/plugins/manifest.schema.json first, then the rules
// across fields, the same rules in the same order as the Rust, Python and TypeScript
// validators. A valid manifest normalises to one JSON text, byte for byte the same in all
// four (the corpus in shared/plugins/testdata/manifests proves it).
package pluginmanifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	plugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

// Error codes (words in web/src/i18n/{en,ar}.json as plugins.error.<code>).
const (
	CodeInvalid             = "manifest_invalid"
	CodeUnknownKey          = "manifest_unknown_key"
	CodeMissingKey          = "manifest_missing_key"
	CodeSchemaNewer         = "manifest_schema_newer"
	CodeReservedID          = "manifest_reserved_id"
	CodeBadPlatform         = "manifest_bad_platform"
	CodeBadRange            = "manifest_bad_range"
	CodeBadCapability       = "manifest_bad_capability"
	CodeDuplicateCapability = "manifest_duplicate_capability"
	CodeDuplicateStage      = "manifest_duplicate_stage"
	CodeBadRequest          = "manifest_bad_request"
	CodeUnknownEvent        = "manifest_unknown_event"
	CodeSubjectMismatch     = "manifest_subject_mismatch"
	CodeBadSchedule         = "manifest_bad_schedule"
	CodeMissingString       = "manifest_missing_string"
	CodeStringTooLong       = "manifest_string_too_long"
	CodeMissingFile         = "manifest_missing_file"
	CodeBadSettings         = "manifest_bad_settings"
	CodeNotTOML             = "manifest_not_toml"
	// CodeTrustedField: a key, a stage name or a subject only Maktaba's own
	// release-signed plugins may use (story 54.22 §1)
	CodeTrustedField           = "plugin_trusted_field"
	MaxNameCodePoints          = 40
	MaxSummaryCodePoints       = 140
	SchemaVersion        int64 = 1
)

// Error is one refusal: a code, where (a slash path into the manifest) and a detail
// (a language, a file); never the plugin's own words.
type Error struct {
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func (e Error) Error() string {
	s := e.Code
	if e.Path != "" {
		s += " at " + e.Path
	}
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// Errors is every refusal of one manifest.
type Errors []Error

func (es Errors) Error() string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// Has reports whether code is among them.
func (es Errors) Has(code string) bool {
	for _, e := range es {
		if e.Code == code {
			return true
		}
	}
	return false
}

// Capability is one entry of capabilities.toml.
type Capability struct {
	Name      string   `toml:"name" json:"name"`
	Risk      string   `toml:"risk" json:"risk"`
	Interface string   `toml:"interface" json:"interface"`
	Functions []string `toml:"functions" json:"functions"`
	Param     string   `toml:"param" json:"param,omitempty"`
	TitleKey  string   `toml:"title_key" json:"title_key"`
	DetailKey string   `toml:"detail_key" json:"detail_key"`
	Since     string   `toml:"since" json:"since"`
}

// Event is one entry of events.toml.
type Event struct {
	Name    string `toml:"name" json:"name"`
	Subject string `toml:"subject" json:"subject"`
	Stage   string `toml:"stage" json:"stage"`
	Map     string `toml:"map" json:"map"`
	Trigger *bool  `toml:"trigger" json:"trigger,omitempty"`
	Since   string `toml:"since" json:"since"`
}

// Triggerable reports whether a [[stage.trigger]] may name it.
func (e Event) Triggerable() bool { return e.Trigger == nil || *e.Trigger }

var (
	loadOnce     sync.Once
	loadErr      error
	schemaRoot   *node
	capabilities []Capability
	capByName    map[string]Capability
	events       []Event
	eventByName  map[string]Event
	idRe         *regexp.Regexp
)

func load() error {
	loadOnce.Do(func() {
		schemaRoot, loadErr = parseSchema(plugins.ManifestSchema)
		if loadErr != nil {
			return
		}
		var caps struct {
			Schema     int          `toml:"schema"`
			Capability []Capability `toml:"capability"`
		}
		md, err := toml.Decode(string(plugins.Capabilities), &caps)
		if err == nil && len(md.Undecoded()) > 0 {
			err = fmt.Errorf("capabilities.toml: unknown keys %v", md.Undecoded())
		}
		if err != nil {
			loadErr = err
			return
		}
		capabilities = caps.Capability
		capByName = map[string]Capability{}
		for _, c := range capabilities {
			capByName[c.Name] = c
		}
		var evs struct {
			Schema int     `toml:"schema"`
			Event  []Event `toml:"event"`
		}
		md, err = toml.Decode(string(plugins.Events), &evs)
		if err == nil && len(md.Undecoded()) > 0 {
			err = fmt.Errorf("events.toml: unknown keys %v", md.Undecoded())
		}
		if err != nil {
			loadErr = err
			return
		}
		events = evs.Event
		eventByName = map[string]Event{}
		for _, e := range events {
			eventByName[e.Name] = e
		}
		idRe = schemaRoot.Properties["id"].re
	})
	return loadErr
}

// Capabilities answers capabilities.toml.
func Capabilities() []Capability {
	if err := load(); err != nil {
		panic(err)
	}
	return capabilities
}

// LookupCapability answers the entry of a granted name ("plugin-data.read:x.y.z" finds
// "plugin-data.read").
func LookupCapability(name string) (Capability, bool) {
	if err := load(); err != nil {
		panic(err)
	}
	base, _, _ := strings.Cut(name, ":")
	c, ok := capByName[base]
	return c, ok
}

// Events answers events.toml.
func Events() []Event {
	if err := load(); err != nil {
		panic(err)
	}
	return events
}

// LookupEvent answers one event.
func LookupEvent(name string) (Event, bool) {
	if err := load(); err != nil {
		panic(err)
	}
	e, ok := eventByName[name]
	return e, ok
}

// ValidID reports whether id is a well-formed plugin id (reserved or not).
func ValidID(id string) bool {
	if err := load(); err != nil {
		panic(err)
	}
	return len(id) <= 100 && idRe.MatchString(id)
}

// Files is what the package rules read besides the manifest: the plugin's files by their
// path in the package (i18n/en.json, ui/settings.schema.json, …). nil skips the package
// rules (a manifest checked on its own).
type Files func(path string) ([]byte, bool)

// Validate parses and checks a manifest.toml. On success it answers the normalised
// manifest (defaults filled); otherwise the refusals.
func Validate(src []byte, files Files) (map[string]any, Errors) { return validate(src, files, false) }

// ValidateTrusted checks the manifest of one of Maktaba's own first-party plugins (story
// 54.22): the keys marked x-trusted-only are read, the id must be under tv.maktaba., and
// its stages keep their exact names. Only manifests built into the release (the
// firstparty package) are ever checked this way.
func ValidateTrusted(src []byte, files Files) (map[string]any, Errors) {
	return validate(src, files, true)
}

func validate(src []byte, files Files, trusted bool) (map[string]any, Errors) {
	if err := load(); err != nil {
		panic(err)
	}
	raw, err := parseTOML(src)
	if err != nil {
		return nil, Errors{{Code: CodeNotTOML, Detail: err.Error()}}
	}
	// a newer schema is refused before anything else: its fields are not known here
	if s, ok := raw["schema"].(int64); ok && s > SchemaVersion {
		return nil, Errors{{Code: CodeSchemaNewer, Path: "schema"}}
	}
	var errs []Error
	schemaRoot.check(raw, "", "", trusted, &errs)
	if len(errs) > 0 {
		return nil, errs
	}
	m := schemaRoot.fill(raw).(map[string]any)
	m["maktaba"] = normaliseRange(m["maktaba"].(string))
	errs = rules(m, files, trusted)
	if len(errs) > 0 {
		return nil, errs
	}
	return m, nil
}

// Normalised is the one JSON text of a valid manifest: keys sorted, no spaces, defaults
// filled, UTF-8 unescaped. The four validators answer it byte for byte the same.
func Normalised(m map[string]any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		panic(err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// QueueName is a plugin stage's job_stages.name (54.01 §4): p_<8 hex of sha256(id)>_<stage>.
// It is the same for every version of the plugin and never clashes with a core stage,
// whose names never start with "p_".
func QueueName(pluginID, stage string) string {
	sum := sha256.Sum256([]byte(pluginID))
	return "p_" + hex.EncodeToString(sum[:])[:8] + "_" + stage
}

func parseTOML(src []byte) (map[string]any, error) {
	if !utf8.Valid(src) {
		return nil, fmt.Errorf("not UTF-8")
	}
	var v map[string]any
	if _, err := toml.NewDecoder(io.LimitReader(bytes.NewReader(src), 1<<20)).Decode(&v); err != nil {
		return nil, err
	}
	out, err := fromTOML(v)
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

func fromTOML(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		o := make(map[string]any, len(t))
		for k, x := range t {
			c, err := fromTOML(x)
			if err != nil {
				return nil, err
			}
			o[k] = c
		}
		return o, nil
	case []map[string]any:
		o := make([]any, len(t))
		for i, x := range t {
			c, err := fromTOML(x)
			if err != nil {
				return nil, err
			}
			o[i] = c
		}
		return o, nil
	case []any:
		o := make([]any, len(t))
		for i, x := range t {
			c, err := fromTOML(x)
			if err != nil {
				return nil, err
			}
			o[i] = c
		}
		return o, nil
	case int64, float64, string, bool:
		return t, nil
	}
	return nil, fmt.Errorf("a %T value is not allowed in a manifest", v)
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
