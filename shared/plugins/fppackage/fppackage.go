// Package fppackage is what makes a package of one of Maktaba's own plugins trusted
// (Epic 54, stories 54.22 §2, 54.27 §2, 54.45 §3), shared by the release's own check
// (api/tools/fp-packages) and the home's install of a package from the store or a file.
//
// A package is one of the plugins of the bundle this binary embeds
// (shared/plugins/firstparty.json): its manifest, strings and statements must be exactly
// the bundle's, stamped with the server's version, and its wheel exactly its plugin's code.
// A Maktaba plugin's statements change only with a server release (54.45 §3): there is no
// other source of SQL a server would run for it.
package fppackage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// The refusals (Epic 51 reason codes too, story 54.37 §3).
const (
	CodeTrustedUnsigned = "plugin_trusted_unsigned"
	CodeTrustedVersion  = "plugin_trusted_version"
)

// Prefix is the id prefix only Maktaba's own plugins use.
const Prefix = "tv.maktaba."

// Error is a refused package.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

func unsigned(format string, a ...any) error {
	return &Error{Code: CodeTrustedUnsigned, Detail: fmt.Sprintf(format, a...)}
}

// Embedded is one plugin of the bundle this binary embeds.
type Embedded struct {
	ID     string
	Folder string
	// Manifest is its normalised manifest (pluginmanifest.ValidateTrusted).
	Manifest map[string]any
	// Component is its [trusted] component.
	Component  string
	Strings    map[string]map[string]string
	Statements []any
}

// Dist is the name of its package's wheel distribution: maktaba_fp_<folder>.
func (e *Embedded) Dist() string { return "maktaba_fp_" + strings.ReplaceAll(e.Folder, "-", "_") }

var (
	bundleOnce sync.Once
	bundle     []*Embedded
	bundleErr  error
)

// Bundle answers the plugins of the embedded bundle, each manifest read as a trusted one.
func Bundle() ([]*Embedded, error) {
	bundleOnce.Do(func() { bundle, bundleErr = parseBundle(sharedplugins.FirstParty) })
	return bundle, bundleErr
}

func parseBundle(raw []byte) ([]*Embedded, error) {
	var doc struct {
		Plugins []struct {
			ID         string                       `json:"id"`
			Folder     string                       `json:"folder"`
			Manifest   string                       `json:"manifest"`
			Strings    map[string]map[string]string `json:"strings"`
			Statements []any                        `json:"statements"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("fppackage: firstparty.json: %w", err)
	}
	out := make([]*Embedded, 0, len(doc.Plugins))
	seen := map[string]bool{}
	for _, p := range doc.Plugins {
		strs := p.Strings
		m, errs := pluginmanifest.ValidateTrusted([]byte(p.Manifest), func(path string) ([]byte, bool) {
			lang, ok := strings.CutPrefix(path, "i18n/")
			if !ok {
				return nil, false
			}
			w, ok := strs[strings.TrimSuffix(lang, ".json")]
			if !ok {
				return nil, false
			}
			b, _ := json.Marshal(w)
			return b, true
		})
		if len(errs) > 0 {
			return nil, fmt.Errorf("fppackage: first-party %s: %w", p.Folder, errs)
		}
		id, _ := m["id"].(string)
		if id != p.ID || seen[id] {
			return nil, fmt.Errorf("fppackage: first-party %s: id %q twice or not its own", p.Folder, id)
		}
		seen[id] = true
		e := &Embedded{ID: id, Folder: p.Folder, Manifest: m, Strings: p.Strings, Statements: p.Statements}
		if t, ok := m["trusted"].(map[string]any); ok {
			e.Component, _ = t["component"].(string)
		}
		if e.Statements == nil {
			e.Statements = []any{}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ByID answers one embedded plugin, nil when the bundle has none of that id.
func ByID(id string) *Embedded {
	l, _ := Bundle()
	for _, e := range l {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// UseBundle replaces the embedded bundle (tests); the returned func restores it.
func UseBundle(raw []byte) func() {
	bundle, bundleErr = parseBundle(raw)
	bundleOnce = sync.Once{}
	bundleOnce.Do(func() {})
	return func() {
		bundleOnce = sync.Once{}
		bundle, bundleErr = nil, nil
	}
}

// Unpacked is a package as componentpack.UnpackPlugin wrote it: its folder, manifest and
// files.json's paths.
type Unpacked struct {
	Dir      string
	Manifest []byte
	Files    []string
}

// open is a file of the package by its files.json path, for pluginsig's digest check; a
// path that is not local to the package is refused.
func (u Unpacked) open(rel string) (io.ReadCloser, error) {
	p := filepath.FromSlash(rel)
	if !filepath.IsLocal(p) {
		return nil, fmt.Errorf("%s is not a path in the package", rel)
	}
	return os.Open(filepath.Join(u.Dir, p))
}

func (u Unpacked) read(rel string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(u.Dir, filepath.FromSlash(rel)))
	return b, err == nil
}

// CheckContents is the package's content against its embedded plugin e, at version (a
// release version, no pre-release): exactly its manifest, strings, statements and its
// wheel <dist>-<version>-py3-none-any.whl; a manifest valid as a trusted one and equal to
// the bundle's but for version and maktaba ("=<version>"); the bundle's strings and
// statements; and a wheel that is its plugin's code (CheckWheel, against pipeline's RECORD
// when the caller has the release's pipeline wheel). It answers the manifest read.
func CheckContents(u Unpacked, e *Embedded, version string, pipeline map[string]string) (map[string]any, error) {
	m, errs := pluginmanifest.ValidateTrusted(u.Manifest, u.read)
	if len(errs) > 0 {
		return nil, errs
	}
	if id, _ := m["id"].(string); id != e.ID {
		return nil, unsigned("its manifest names %q, not %s", id, e.ID)
	}
	if v, _ := m["version"].(string); v != version {
		return nil, &Error{Code: CodeTrustedVersion, Detail: fmt.Sprintf("%s %s is not %s", e.ID, v, version)}
	}
	if m["maktaba"] != "="+version {
		return nil, &Error{Code: CodeTrustedVersion, Detail: fmt.Sprintf("maktaba %v is not =%s", m["maktaba"], version)}
	}
	wheel := "wheels/" + e.Dist() + "-" + version + "-py3-none-any.whl"
	expect := []string{"manifest.toml", "i18n/en.json", "i18n/ar.json", "statements.json", wheel}
	members := map[string]bool{}
	for _, f := range u.Files {
		if members[f] {
			return nil, unsigned("it lists %s more than once", f)
		}
		members[f] = true
	}
	if len(members) != len(expect) {
		return nil, unsigned("it holds %d files, not its manifest, strings, statements and its wheel %s", len(members), wheel)
	}
	for _, x := range expect {
		if !members[x] {
			return nil, unsigned("it has no %s", x)
		}
	}
	stamped := map[string]any{}
	for k, v := range e.Manifest {
		stamped[k] = v
	}
	stamped["version"], stamped["maktaba"] = m["version"], m["maktaba"]
	if !reflect.DeepEqual(stamped, m) {
		return nil, unsigned("its manifest is not the one this server embeds")
	}
	for lang, words := range e.Strings {
		b, ok := u.read("i18n/" + lang + ".json")
		var got map[string]string
		if !ok || checkNoDuplicateKeys(b) != nil || json.Unmarshal(b, &got) != nil || !reflect.DeepEqual(got, words) {
			return nil, unsigned("its i18n/%s.json is not the one this server embeds", lang)
		}
	}
	// a Maktaba plugin's statements change only with a server release (54.45 §3)
	b, ok := u.read("statements.json")
	var sts []any
	if !ok || checkNoDuplicateKeys(b) != nil || json.Unmarshal(b, &sts) != nil || !reflect.DeepEqual(sts, e.Statements) {
		return nil, unsigned("its statements.json is not the one this server embeds")
	}
	whl, _ := u.read(wheel)
	if err := CheckWheel(whl, e.Dist(), version, pipeline); err != nil {
		return nil, unsigned("%s: %v", wheel, err)
	}
	return m, nil
}

// checkNoDuplicateKeys rejects malformed JSON, trailing tokens, and any JSON object
// (at any nesting depth) that repeats a key.
func checkNoDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := checkJSONValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("trailing data after JSON value")
	}
	return nil
}

func checkJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			k, ok := kt.(string)
			if !ok {
				return fmt.Errorf("expected string key")
			}
			if seen[k] {
				return fmt.Errorf("duplicate key %q", k)
			}
			seen[k] = true
			if err := checkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := checkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	default:
		return fmt.Errorf("unexpected delimiter %q", d)
	}
}
