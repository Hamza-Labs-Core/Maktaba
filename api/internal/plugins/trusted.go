package plugins

// Maktaba's own first-party plugins (Epic 54, story 54.22): the trusted manifest and what
// makes a package trusted.
//
// The first-party plugins this server may run are the ones built into it: the bundle
// shared/plugins/firstparty.json (tools/plugins/bundle.py) is embedded in this binary,
// which the release signs, and the same bytes ride in the release's signed pipeline wheel
// for the runner. Only a manifest from the embedded bundle is read with its [trusted]
// table (pluginmanifest.ValidateTrusted), its version is this server's, and its
// statements are the only SQL the plugin API ever runs for it. There is no setting, flag
// or environment variable that marks anything else trusted.
//
// A first-party package found on disk (the release's payload, story 54.27 §2) is checked
// by CheckPackage against the release's verified checksums: its files.json digest must be
// listed there, its version must be the server's and its id under tv.maktaba.; otherwise
// plugin_trusted_unsigned or plugin_trusted_version, and it is refused.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/statements"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// The trust refusals (Epic 51 reason codes too, story 54.37 §3).
const (
	CodeTrustedUnsigned = "plugin_trusted_unsigned"
	CodeTrustedVersion  = "plugin_trusted_version"
)

// FirstPartyPrefix is the id prefix only Maktaba's own plugins use.
const FirstPartyPrefix = "tv.maktaba."

// FirstParty is one of Maktaba's own plugins as this release built it.
type FirstParty struct {
	ID      string
	Folder  string
	Version string
	Digest  string
	// Manifest is the normalised manifest (defaults filled).
	Manifest   map[string]any
	Trusted    Trusted
	Stages     []string
	Strings    map[string]map[string]string
	Statements []statements.Statement
}

// Trusted is a manifest's [trusted] table (README §15.4).
type Trusted struct {
	Runner     string   `json:"runner"`
	Component  string   `json:"component"`
	DBRead     []string `json:"db_read"`
	DBWrite    []string `json:"db_write"`
	Statements string   `json:"statements"`
	Settings   []string `json:"settings"`
	FilesRead  []string `json:"files_read"`
	FilesWrite []string `json:"files_write"`
	Exec       []string `json:"exec"`
	GPU        string   `json:"gpu"`
	Net        []string `json:"net"`
	Service    []string `json:"service"`
	Compute    []string `json:"compute"`
}

// Capabilities answers what the Plugins page's "What it can do" sheet lists, as
// fp.trusted.<capability> keys with their parameter: the trusted set, then the public
// capabilities (story 54.22 §4).
func (fp *FirstParty) Capabilities() []map[string]string {
	var out []map[string]string
	add := func(k, p string) { out = append(out, map[string]string{"key": "fp.trusted." + k, "param": p}) }
	if len(fp.Trusted.DBRead)+len(fp.Trusted.DBWrite) > 0 {
		add("db.statements", strings.Join(fp.Trusted.DBWrite, ", "))
	}
	for _, s := range fp.Trusted.Settings {
		add("settings.read", s)
	}
	for _, a := range fp.Trusted.FilesRead {
		add("files.read", a)
	}
	for _, a := range fp.Trusted.FilesWrite {
		add("files.write", a)
	}
	for _, x := range fp.Trusted.Exec {
		add("exec", x)
	}
	if fp.Trusted.GPU != "" && fp.Trusted.GPU != "none" {
		add("gpu", fp.Trusted.GPU)
	}
	if len(fp.Trusted.Net) > 0 {
		add("net", strings.Join(fp.Trusted.Net, ", "))
	}
	for _, s := range fp.Trusted.Service {
		add("service", s)
	}
	for _, c := range fp.Trusted.Compute {
		add("compute."+c, "")
	}
	add("graph.internal", "")
	if caps, ok := fp.Manifest["capabilities"].(map[string]any); ok {
		for _, list := range []string{"required", "optional"} {
			l, _ := caps[list].([]any)
			for _, c := range l {
				out = append(out, map[string]string{"key": "plugins.cap." + fmt.Sprint(c), "param": ""})
			}
		}
	}
	return out
}

type bundleDoc struct {
	Schema  int    `json:"schema"`
	Digest  string `json:"digest"`
	Plugins []struct {
		ID         string                       `json:"id"`
		Folder     string                       `json:"folder"`
		Manifest   string                       `json:"manifest"`
		Strings    map[string]map[string]string `json:"strings"`
		Statements []statements.Statement       `json:"statements"`
		Digest     string                       `json:"digest"`
	} `json:"plugins"`
}

var (
	fpOnce   sync.Once
	fpList   []*FirstParty
	fpErr    error
	fpDigest string
)

// FirstPartyPlugins answers the plugins of the embedded bundle, each checked with
// ValidateTrusted (a bundle that fails is a broken build: none is answered, and the error
// says why).
func FirstPartyPlugins() ([]*FirstParty, error) {
	fpOnce.Do(func() { fpList, fpDigest, fpErr = parseBundle(sharedplugins.FirstParty) })
	return fpList, fpErr
}

// BundleDigest is the embedded bundle's digest: the runner's copy must have the same
// (story 54.23 §1 step 1).
func BundleDigest() string {
	_, _ = FirstPartyPlugins()
	return fpDigest
}

func parseBundle(raw []byte) ([]*FirstParty, string, error) {
	var doc bundleDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, "", fmt.Errorf("plugins: firstparty.json: %w", err)
	}
	var out []*FirstParty
	seen := map[string]bool{}
	for _, p := range doc.Plugins {
		files := func(path string) ([]byte, bool) {
			lang, ok := strings.CutPrefix(path, "i18n/")
			if !ok {
				return nil, false
			}
			words, ok := p.Strings[strings.TrimSuffix(lang, ".json")]
			if !ok {
				return nil, false
			}
			b, _ := json.Marshal(words)
			return b, true
		}
		m, errs := pluginmanifest.ValidateTrusted([]byte(p.Manifest), files)
		if len(errs) > 0 {
			return nil, "", fmt.Errorf("plugins: first-party %s: %w", p.Folder, errs)
		}
		id, _ := m["id"].(string)
		if id != p.ID || seen[id] {
			return nil, "", fmt.Errorf("plugins: first-party %s: id %q twice or not its own", p.Folder, id)
		}
		seen[id] = true
		fp := &FirstParty{ID: id, Folder: p.Folder, Digest: p.Digest, Manifest: m, Strings: p.Strings, Statements: p.Statements}
		fp.Version, _ = m["version"].(string)
		fp.Trusted = trustedOf(m)
		for _, s := range m["stage"].([]any) {
			fp.Stages = append(fp.Stages, s.(map[string]any)["name"].(string))
		}
		out = append(out, fp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, doc.Digest, nil
}

func strs(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func trustedOf(m map[string]any) Trusted {
	t, _ := m["trusted"].(map[string]any)
	db, _ := t["db"].(map[string]any)
	files, _ := t["files"].(map[string]any)
	s := func(k string) string { v, _ := t[k].(string); return v }
	return Trusted{Runner: s("runner"), Component: s("component"), DBRead: strs(db["read"]), DBWrite: strs(db["write"]),
		Statements: s("statements"), Settings: strs(t["settings"]), FilesRead: strs(files["read"]),
		FilesWrite: strs(files["write"]), Exec: strs(t["exec"]), GPU: s("gpu"), Net: strs(t["net"]),
		Service: strs(t["service"]), Compute: strs(t["compute"])}
}

// FirstPartyByID answers one embedded first-party plugin.
func FirstPartyByID(id string) *FirstParty {
	l, _ := FirstPartyPlugins()
	for _, fp := range l {
		if fp.ID == id {
			return fp
		}
	}
	return nil
}

// FirstPartyByStage answers the first-party plugin that defines a stage.
func FirstPartyByStage(stage string) *FirstParty {
	l, _ := FirstPartyPlugins()
	for _, fp := range l {
		for _, s := range fp.Stages {
			if s == stage {
				return fp
			}
		}
	}
	return nil
}

// Package is a first-party package found on disk: its manifest's id and version, and the
// sha256 of its files.json.
type Package struct {
	ID        string
	Version   string
	FilesJSON []byte
}

// CheckPackage is story 54.22 §2: a first-party package is trusted when its files.json
// digest is in the running release's verified checksums (under any name), its version is
// the server's, and its id is under tv.maktaba.. The verified checksums come from
// releaseverify (the updater's own check); nil checksums trust nothing.
func CheckPackage(p Package, checksums releaseverify.Checksums, serverVersion string) error {
	if !strings.HasPrefix(p.ID, FirstPartyPrefix) {
		return &Refusal{Status: 403, Code: CodeTrustedUnsigned, Detail: p.ID + " is not a Maktaba id"}
	}
	sum := sha256.Sum256(p.FilesJSON)
	digest := hex.EncodeToString(sum[:])
	listed := false
	for _, d := range checksums {
		if strings.EqualFold(d, digest) {
			listed = true
		}
	}
	if !listed {
		return &Refusal{Status: 403, Code: CodeTrustedUnsigned, Detail: p.ID + ": files.json is not in the release's checksums"}
	}
	if p.Version != serverVersion {
		return &Refusal{Status: 403, Code: CodeTrustedVersion, Detail: p.ID + " " + p.Version + " is not " + serverVersion}
	}
	return nil
}
