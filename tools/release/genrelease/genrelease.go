// Package genrelease builds release.json (Epic 35 design §3.1) from a release's
// built assets and the repository it was built from, and checks one against the
// repository (story 35.1 AC2). The types are releaseverify's, so the file the
// release workflow writes is exactly the file every machine parses.
package genrelease

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Repository paths read from the checkout.
const (
	MigrationsDir   = "shared/db/migrations"
	ManifestMD      = "shared/db/migrations/MANIFEST.md"
	SchemaPy        = "pipeline/src/maktaba_pipeline/cluster/schema.py"
	SchemaGo        = "streaming/internal/cluster/schema.go"
	EdgeProtocolGo  = "streaming/internal/frontdoor/protocol.go"
	AgentProtocolGo = "api/internal/cluster/agent/crypto.go"
	LauncherGo      = "cmd/maktaba-launcher/main.go"
	PythonVersion   = "pipeline/.python-version"

	// MarkerOlderNodes and MarkerPreviousRelease are the MANIFEST.md markers
	// (Epic 31 §5.1; Epic 35 design §6.3).
	MarkerOlderNodes      = "Breaking for older nodes"
	MarkerPreviousRelease = "Breaking for the previous release"
)

// Options are the inputs that do not come from the checkout.
type Options struct {
	Tag              string
	Commit           string
	Channel          string    // "" → stable, or beta for a prerelease tag
	Published        time.Time // zero → now
	Dist             string    // directory holding the built assets
	Repo             string    // checkout root
	LauncherProtocol int       // 0 → cmd/maktaba-launcher's LauncherProtocol
	// Previous is the previous release's release.json (nil for the first one):
	// its schema_rev bounds breaks_previous_home, its min_upgrade_from carries over.
	Previous *rv.Manifest
	// MinUpgradeFrom overrides the carried-over value.
	MinUpgradeFrom string
}

// RepoFacts are what release.json takes from the checkout.
type RepoFacts struct {
	SchemaRev          int
	MinNodeSchema      int
	BreaksPreviousHome bool
	EdgeProtocol       int
	AgentProtocol      int
	LauncherProtocol   int
	Python             string
}

var (
	archiveRe  = regexp.MustCompile(`^maktaba-server-(.+)-([a-z0-9]+)-([a-z0-9]+)\.(tar\.gz|zip)$`)
	slotRe     = regexp.MustCompile(`^(\d{4})_[A-Za-z0-9_]+\.sql$`)
	rowRe      = regexp.MustCompile("^\\|\\s*`(\\d{4})`\\s*\\|")
	returnsRe  = regexp.MustCompile(`maktaba_min_node_schema\(\)` + "`?" + `\s+returns\s+(\d+)`)
	schemaPyRe = regexp.MustCompile(`(?m)^SCHEMA_REV\s*(?::\s*int\s*)?=\s*(\d+)\s*$`)
	schemaGoRe = regexp.MustCompile(`(?m)^const SchemaRev\s*=\s*(\d+)\s*$`)
	edgeRe     = regexp.MustCompile(`(?m)^const EdgeProtocol\s*=\s*(\d+)\s*$`)
	launcherRe = regexp.MustCompile(`(?m)^const LauncherProtocol\s*=\s*(\d+)\s*$`)
	agentRe    = regexp.MustCompile(`(?m)^\s*ProtocolVersion\s*=\s*(\d+)\s*$`)
)

// ReadRepo derives the schema and protocol facts from a checkout. previousRev
// is the previous release's schema_rev (0: none). Disagreement between the
// migrations, SCHEMA_REV and SchemaRev is an error (AC2).
func ReadRepo(repo string, previousRev int) (RepoFacts, error) {
	var f RepoFacts
	entries, err := os.ReadDir(filepath.Join(repo, MigrationsDir))
	if err != nil {
		return f, err
	}
	for _, e := range entries {
		if m := slotRe.FindStringSubmatch(e.Name()); m != nil && !strings.HasSuffix(e.Name(), ".sqlite.sql") {
			n, _ := strconv.Atoi(m[1])
			if n > f.SchemaRev {
				f.SchemaRev = n
			}
		}
	}
	if f.SchemaRev == 0 {
		return f, errors.New("no migrations found")
	}
	py, err := intConst(filepath.Join(repo, SchemaPy), schemaPyRe)
	if err != nil {
		return f, err
	}
	gov, err := intConst(filepath.Join(repo, SchemaGo), schemaGoRe)
	if err != nil {
		return f, err
	}
	if py != f.SchemaRev || gov != f.SchemaRev {
		return f, fmt.Errorf("schema revisions disagree: highest migration slot %d, SCHEMA_REV (%s) %d, SchemaRev (%s) %d",
			f.SchemaRev, SchemaPy, py, SchemaGo, gov)
	}
	md, err := os.ReadFile(filepath.Join(repo, ManifestMD))
	if err != nil {
		return f, err
	}
	f.MinNodeSchema, f.BreaksPreviousHome, err = markers(string(md), previousRev)
	if err != nil {
		return f, err
	}
	if f.EdgeProtocol, err = intConst(filepath.Join(repo, EdgeProtocolGo), edgeRe); err != nil {
		return f, err
	}
	if f.AgentProtocol, err = intConst(filepath.Join(repo, AgentProtocolGo), agentRe); err != nil {
		return f, err
	}
	if f.LauncherProtocol, err = intConst(filepath.Join(repo, LauncherGo), launcherRe); err != nil {
		return f, err
	}
	pv, err := os.ReadFile(filepath.Join(repo, PythonVersion))
	if err != nil {
		return f, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(pv)), ".", 3)
	if len(parts) < 2 {
		return f, fmt.Errorf("%s: want MAJOR.MINOR", PythonVersion)
	}
	f.Python = parts[0] + "." + parts[1]
	return f, nil
}

// markers reads MANIFEST.md: min_node_schema is the highest value a
// "Breaking for older nodes" row makes maktaba_min_node_schema() return (its
// slot when it names none); breaks_previous_home is any "Breaking for the
// previous release" row above previousRev.
func markers(md string, previousRev int) (minNode int, breaksPrev bool, err error) {
	for _, line := range strings.Split(md, "\n") {
		m := rowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slot, _ := strconv.Atoi(m[1])
		if strings.Contains(line, MarkerOlderNodes) {
			v := slot
			if r := returnsRe.FindStringSubmatch(line); r != nil {
				v, _ = strconv.Atoi(r[1])
			}
			if v > slot {
				return 0, false, fmt.Errorf("MANIFEST slot %04d: min node schema %d is above its own slot", slot, v)
			}
			if v > minNode {
				minNode = v
			}
		}
		if strings.Contains(line, MarkerPreviousRelease) && slot > previousRev {
			breaksPrev = true
		}
	}
	return minNode, breaksPrev, nil
}

func intConst(file string, re *regexp.Regexp) (int, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	m := re.FindAllSubmatch(b, -1)
	if len(m) != 1 {
		return 0, fmt.Errorf("%s: want exactly one %s, found %d", file, re, len(m))
	}
	return strconv.Atoi(string(m[0][1]))
}

// Build writes nothing: it returns release.json for o.
func Build(o Options) (*rv.Manifest, error) {
	if !rv.ValidTag(o.Tag) {
		return nil, fmt.Errorf("invalid tag %q", o.Tag)
	}
	version := strings.TrimPrefix(o.Tag, "v")
	prevRev := 0
	if o.Previous != nil {
		prevRev = o.Previous.SchemaRev
	}
	facts, err := ReadRepo(o.Repo, prevRev)
	if err != nil {
		return nil, err
	}
	channel := o.Channel
	if channel == "" {
		channel = "stable"
		if strings.Contains(version, "-") {
			channel = "beta"
		}
	}
	published := o.Published
	if published.IsZero() {
		published = time.Now()
	}
	launcher := o.LauncherProtocol
	if launcher == 0 {
		launcher = facts.LauncherProtocol // cmd/maktaba-launcher LauncherProtocol
	}
	minUp := o.MinUpgradeFrom
	if minUp == "" && o.Previous != nil {
		minUp = o.Previous.MinUpgradeFrom
	}
	if minUp == "" {
		// The first release with release.json: nothing older updates to it by
		// itself (plan 35.1 §4).
		minUp = version
	}
	m := &rv.Manifest{
		V: rv.ManifestVersion, Version: version, Tag: o.Tag, Commit: strings.ToLower(o.Commit), Channel: channel,
		Published: published.UTC().Truncate(time.Second), SchemaRev: facts.SchemaRev, MinNodeSchema: facts.MinNodeSchema,
		BreaksPreviousHome: facts.BreaksPreviousHome, MinUpgradeFrom: minUp,
		EdgeProtocol: facts.EdgeProtocol, LauncherProtocol: launcher, AgentProtocol: facts.AgentProtocol,
		Assets: map[string]rv.Asset{},
	}
	entries, err := os.ReadDir(o.Dist)
	if err != nil {
		return nil, err
	}
	var wheel, cons string
	for _, e := range entries {
		name := e.Name()
		switch {
		case archiveRe.MatchString(name):
			p := archiveRe.FindStringSubmatch(name)
			if p[1] != version {
				return nil, fmt.Errorf("%s: archive version %s, tag %s", name, p[1], o.Tag)
			}
			a, err := archiveAsset(filepath.Join(o.Dist, name), name)
			if err != nil {
				return nil, err
			}
			key := p[2] + "/" + p[3]
			if _, dup := m.Assets[key]; dup {
				return nil, fmt.Errorf("two archives for %s", key)
			}
			m.Assets[key] = a
		case strings.HasPrefix(name, "maktaba_pipeline-") && strings.HasSuffix(name, ".whl"):
			if wheel != "" {
				return nil, fmt.Errorf("two pipeline wheels: %s, %s", wheel, name)
			}
			wheel = name
		case strings.HasPrefix(name, "maktaba-pipeline-") && strings.HasSuffix(name, "-constraints.txt"):
			if cons != "" {
				return nil, fmt.Errorf("two constraints files: %s, %s", cons, name)
			}
			cons = name
		}
	}
	if len(m.Assets) == 0 {
		return nil, fmt.Errorf("no maktaba-server-%s-<os>-<arch> archives in %s", version, o.Dist)
	}
	if (wheel == "") != (cons == "") {
		return nil, errors.New("pipeline wheel and constraints must ship together")
	}
	if wheel != "" {
		wd, err := fileSHA(filepath.Join(o.Dist, wheel))
		if err != nil {
			return nil, err
		}
		cb, err := os.ReadFile(filepath.Join(o.Dist, cons))
		if err != nil {
			return nil, err
		}
		if err := rv.CheckConstraints(cons, cb); err != nil {
			return nil, err
		}
		m.Pipeline = &rv.Pipeline{Wheel: wheel, WheelSHA256: wd, Constraints: cons, ConstraintsSHA256: hexSHA(cb), Python: facts.Python}
	}
	return m, nil
}

// Check compares a release.json with the checkout (AC2 / TC9 release-manifest-check).
func Check(m *rv.Manifest, repo string, previousRev int) error {
	facts, err := ReadRepo(repo, previousRev)
	if err != nil {
		return err
	}
	var errs []string
	if m.SchemaRev != facts.SchemaRev {
		errs = append(errs, fmt.Sprintf("schema_rev %d, SCHEMA_REV/SchemaRev %d", m.SchemaRev, facts.SchemaRev))
	}
	if m.MinNodeSchema != facts.MinNodeSchema {
		errs = append(errs, fmt.Sprintf("min_node_schema %d, MANIFEST %q rows say %d", m.MinNodeSchema, MarkerOlderNodes, facts.MinNodeSchema))
	}
	if m.BreaksPreviousHome != facts.BreaksPreviousHome {
		errs = append(errs, fmt.Sprintf("breaks_previous_home %v, MANIFEST %q rows say %v", m.BreaksPreviousHome, MarkerPreviousRelease, facts.BreaksPreviousHome))
	}
	if m.EdgeProtocol != facts.EdgeProtocol || m.AgentProtocol != facts.AgentProtocol {
		errs = append(errs, fmt.Sprintf("edge/agent protocol %d/%d, source says %d/%d", m.EdgeProtocol, m.AgentProtocol, facts.EdgeProtocol, facts.AgentProtocol))
	}
	if m.LauncherProtocol != facts.LauncherProtocol {
		errs = append(errs, fmt.Sprintf("launcher_protocol %d, %s says %d", m.LauncherProtocol, LauncherGo, facts.LauncherProtocol))
	}
	if len(errs) > 0 {
		return fmt.Errorf("release.json disagrees with the repository: %s", strings.Join(errs, "; "))
	}
	return nil
}

// archiveAsset hashes the archive and the maktaba-* binaries inside it.
func archiveAsset(p, name string) (rv.Asset, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return rv.Asset{}, err
	}
	files := map[string]string{}
	add := func(entry string, r io.Reader) error {
		base := path.Base(strings.ReplaceAll(entry, "\\", "/"))
		if !strings.HasPrefix(base, "maktaba-") || strings.HasSuffix(base, ".service") {
			return nil
		}
		h := sha256.New()
		if _, err := io.Copy(h, r); err != nil {
			return err
		}
		if _, dup := files[base]; dup {
			return fmt.Errorf("%s: %s twice", name, base)
		}
		files[base] = hex.EncodeToString(h.Sum(nil))
		return nil
	}
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return rv.Asset{}, fmt.Errorf("%s: %w", name, err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return rv.Asset{}, err
			}
			err = add(f.Name, rc)
			_ = rc.Close()
			if err != nil {
				return rv.Asset{}, err
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return rv.Asset{}, fmt.Errorf("%s: %w", name, err)
		}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return rv.Asset{}, fmt.Errorf("%s: %w", name, err)
			}
			if hdr.Typeflag == tar.TypeReg {
				if err := add(hdr.Name, tr); err != nil {
					return rv.Asset{}, err
				}
			}
		}
	}
	if len(files) == 0 {
		return rv.Asset{}, fmt.Errorf("%s: no maktaba-* binaries inside", name)
	}
	return rv.Asset{Archive: name, SHA256: hexSHA(b), Files: files}, nil
}

func hexSHA(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func fileSHA(p string) (string, error) {
	return rv.SHA256File(p)
}
