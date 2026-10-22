package releaseverify

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Release file names (design §3.1).
const (
	ChecksumsFile = "checksums.txt"
	BundleFile    = "checksums.txt.sigstore.json"
	ManifestFile  = "release.json"
	// ManifestVersion is the release.json schema this package reads ("v").
	ManifestVersion = 1
)

// Manifest is release.json (design §3.1). It is not signed on its own: its
// digest is in checksums.txt, which the Sigstore bundle signs.
type Manifest struct {
	V                  int              `json:"v"`
	Version            string           `json:"version"`
	Tag                string           `json:"tag"`
	Commit             string           `json:"commit"`
	Channel            string           `json:"channel"`
	Published          time.Time        `json:"published"`
	SchemaRev          int              `json:"schema_rev"`
	MinNodeSchema      int              `json:"min_node_schema"`
	BreaksPreviousHome bool             `json:"breaks_previous_home"`
	MinUpgradeFrom     string           `json:"min_upgrade_from"`
	EdgeProtocol       int              `json:"edge_protocol"`
	LauncherProtocol   int              `json:"launcher_protocol"`
	AgentProtocol      int              `json:"agent_protocol"`
	Assets             map[string]Asset `json:"assets"`
	Pipeline           *Pipeline        `json:"pipeline,omitempty"`
}

// Asset is one platform's server archive and the digests of the binaries in it.
type Asset struct {
	Archive string            `json:"archive"`
	SHA256  string            `json:"sha256"`
	Files   map[string]string `json:"files"`
}

// Pipeline is the pipeline wheel and its hash-checked constraints. ConstraintsCUDA
// (story 38.9, absent before): the same pins with torch's CUDA build for Windows, for a
// machine whose NVIDIA GPU runs the second listen (the default set has PyPI's CPU build).
type Pipeline struct {
	Wheel                 string `json:"wheel"`
	WheelSHA256           string `json:"wheel_sha256"`
	Constraints           string `json:"constraints"`
	ConstraintsSHA256     string `json:"constraints_sha256"`
	ConstraintsCUDA       string `json:"constraints_cuda,omitempty"`
	ConstraintsCUDASHA256 string `json:"constraints_cuda_sha256,omitempty"`
	Python                string `json:"python"`
}

// IsConstraints reports whether name is one of the pipeline's constraints files.
func (p *Pipeline) IsConstraints(name string) bool {
	return p != nil && name != "" && (name == p.Constraints || name == p.ConstraintsCUDA)
}

// Checksums maps a release file name to its lowercase hex SHA-256.
type Checksums map[string]string

var (
	hexRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitRe   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	platformRe = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)
	commentRe  = regexp.MustCompile(`(^|\s)#`)
)

// ParseChecksums reads a sha256sum-format file ("<hex>  <name>", optional "*"
// binary marker and "./" prefix). Duplicate names, non-hex digests, names with
// a path and malformed lines are ErrManifest (story 35.1 EC1), never
// "first wins".
func ParseChecksums(b []byte) (Checksums, error) {
	out := Checksums{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		digest, name, ok := strings.Cut(text, " ")
		if !ok {
			return nil, fail(ErrManifest, ChecksumsFile, fmt.Sprintf("line %d: not \"<sha256>  <name>\"", line), nil)
		}
		name = strings.TrimPrefix(strings.TrimLeft(name, " "), "*")
		name = strings.TrimPrefix(name, "./")
		if !hexRe.MatchString(digest) {
			return nil, fail(ErrManifest, ChecksumsFile, fmt.Sprintf("line %d: digest is not 64 lowercase hex characters", line), nil)
		}
		if !validFileName(name) {
			return nil, fail(ErrManifest, ChecksumsFile, fmt.Sprintf("line %d: bad file name %q", line, name), nil)
		}
		if _, dup := out[name]; dup {
			return nil, fail(ErrManifest, ChecksumsFile, fmt.Sprintf("line %d: %s listed twice", line, name), nil)
		}
		out[name] = digest
	}
	if err := sc.Err(); err != nil {
		return nil, fail(ErrManifest, ChecksumsFile, "read", err)
	}
	if len(out) == 0 {
		return nil, fail(ErrManifest, ChecksumsFile, "empty", nil)
	}
	return out, nil
}

// Names returns the listed file names, sorted.
func (c Checksums) Names() []string {
	out := make([]string, 0, len(c))
	for n := range c {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func validFileName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, "/\\\x00 \t") && !strings.HasPrefix(name, "-")
}

// ParseManifest decodes release.json. Unknown fields are allowed (a later minor
// addition); a different "v" is not.
func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fail(ErrManifest, ManifestFile, "decode", err)
	}
	if m.V != ManifestVersion {
		return nil, fail(ErrManifest, ManifestFile, fmt.Sprintf("unsupported schema v=%d (want %d)", m.V, ManifestVersion), nil)
	}
	return &m, nil
}

// Validate checks release.json against the tag and the signed checksums: the
// version and tag fields, every digest's form, and that each archive, wheel and
// constraints digest equals the one checksums.txt lists (design §3.2 step 4).
func (m *Manifest) Validate(tag string, sums Checksums) error {
	bad := func(format string, a ...any) error {
		return fail(ErrManifest, ManifestFile, fmt.Sprintf(format, a...), nil)
	}
	if m.Tag != tag {
		return bad("tag %q, expected %q", m.Tag, tag)
	}
	if m.Version != strings.TrimPrefix(tag, "v") {
		return bad("version %q does not match tag %q", m.Version, tag)
	}
	if !commitRe.MatchString(m.Commit) {
		return bad("commit %q is not a 40-hex git sha", m.Commit)
	}
	if m.SchemaRev < 0 || m.MinNodeSchema < 0 || m.MinNodeSchema > m.SchemaRev {
		return bad("schema_rev %d / min_node_schema %d inconsistent", m.SchemaRev, m.MinNodeSchema)
	}
	if m.MinUpgradeFrom != "" && CompareVersions(m.MinUpgradeFrom, m.Version) > 0 {
		return bad("min_upgrade_from %s is newer than %s", m.MinUpgradeFrom, m.Version)
	}
	if len(m.Assets) == 0 {
		return bad("no assets")
	}
	for platform, a := range m.Assets {
		if !platformRe.MatchString(platform) {
			return bad("platform %q is not os/arch", platform)
		}
		if !validFileName(a.Archive) || !hexRe.MatchString(a.SHA256) {
			return bad("assets[%s]: bad archive name or digest", platform)
		}
		if got, ok := sums[a.Archive]; !ok || got != a.SHA256 {
			return bad("assets[%s]: %s digest differs from checksums.txt", platform, a.Archive)
		}
		if len(a.Files) == 0 {
			return bad("assets[%s]: no files", platform)
		}
		for name, d := range a.Files {
			if !validFileName(name) || !hexRe.MatchString(d) {
				return bad("assets[%s].files: bad entry %q", platform, name)
			}
		}
	}
	if p := m.Pipeline; p != nil {
		files := []struct{ name, digest string }{{p.Wheel, p.WheelSHA256}, {p.Constraints, p.ConstraintsSHA256}}
		if p.ConstraintsCUDA != "" || p.ConstraintsCUDASHA256 != "" {
			files = append(files, struct{ name, digest string }{p.ConstraintsCUDA, p.ConstraintsCUDASHA256})
		}
		for _, f := range files {
			if !validFileName(f.name) || !hexRe.MatchString(f.digest) {
				return bad("pipeline: bad entry %q", f.name)
			}
			if got, ok := sums[f.name]; !ok || got != f.digest {
				return bad("pipeline: %s digest differs from checksums.txt", f.name)
			}
		}
	}
	return nil
}

// VerifyExtracted checks one binary taken out of the platform archive against
// release.json's assets[platform].files (design §3.2 step 6).
func (m *Manifest) VerifyExtracted(platform, name string, data []byte) error {
	a, ok := m.Assets[platform]
	if !ok {
		return fail(ErrManifest, ManifestFile, "no assets for "+platform, nil)
	}
	want, ok := a.Files[name]
	if !ok {
		return fail(ErrChecksum, name, "not listed in release.json assets["+platform+"].files", nil)
	}
	if got := sha256Hex(data); got != want {
		return fail(ErrChecksum, name, fmt.Sprintf("sha256 %s, release.json lists %s", got, want), nil)
	}
	return nil
}

// CheckConstraints enforces the PR #94 rule: every requirement in a pip
// constraints file carries --hash=sha256:. Comments, blank lines and option
// lines are skipped; "\" continuations are joined.
func CheckConstraints(name string, b []byte) error {
	var logical []string
	var cur strings.Builder
	for _, raw := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		line := raw
		// pip strips a comment that starts the line or follows any whitespace
		// (its COMMENT_RE is `(^|\s+)#.*$`), tab included: "foo==1<TAB>#--hash=…"
		// has no hash for pip and must have none here.
		if loc := commentRe.FindStringIndex(line); loc != nil {
			line = line[:loc[0]]
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "\\") {
			cur.WriteString(strings.TrimSuffix(trimmed, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(trimmed)
		logical = append(logical, strings.TrimSpace(cur.String()))
		cur.Reset()
	}
	if rest := strings.TrimSpace(cur.String()); rest != "" {
		logical = append(logical, rest)
	}
	n := 0
	for _, l := range logical {
		if l == "" || strings.HasPrefix(l, "-") {
			continue // blank or an option line (--index-url, -c, …)
		}
		n++
		if !strings.Contains(l, "--hash=sha256:") {
			pkg, _, _ := strings.Cut(l, " ")
			return fail(ErrNoHashes, name, pkg, nil)
		}
	}
	if n == 0 {
		return fail(ErrNoHashes, name, "no requirements", nil)
	}
	return nil
}

// CompareVersions compares release versions ("1.5.0", "v1.5.0", "1.5.0-rc.1"):
// numeric core first, then a prerelease ranks below its release; prereleases of
// the same core compare by their dot-separated identifiers (numeric ones
// numerically). Unparseable cores compare as empty, below every release. A
// `git describe` build ranks after the tag it was built from (a machine running
// one is not behind that release), by its commits after the tag, then dirty
// after clean: v0.2.0 < v0.2.0-dirty < v0.2.0-1-gabc1234 <
// v0.2.0-1-gabc1234-dirty < v0.2.1.
func CompareVersions(a, b string) int {
	ta, na, da := splitDescribe(a)
	tb, nb, db := splitDescribe(b)
	if c := compareReleases(ta, tb); c != 0 {
		return c
	}
	if c := cmpInt(na, nb); c != 0 {
		return c
	}
	switch {
	case da == db:
		return 0
	case da:
		return 1
	}
	return -1
}

// IsPrerelease reports whether v is a prerelease, or a `git describe` build of
// one (1.0.0-rc.1, 1.0.0-rc.1-3-g1a2b3c4d); a build of a release tag
// (0.2.0-12-gabc1234) is not.
func IsPrerelease(v string) bool {
	tag, _, _ := splitDescribe(v)
	_, pre := splitVersion(tag)
	return pre != ""
}

// describeBuild and splitDescribe are layout.SplitDescribe's (this package
// does not import layout, so the modules that verify releases need not): the
// release tag (normalized: no "v", no build metadata), the commits after it,
// whether the tree was dirty.
var describeBuild = regexp.MustCompile(`^(\d+\.\d+\.\d+(?:-[0-9a-z.]+)?)-(\d+)-g[0-9a-f]{4,40}$`)

func splitDescribe(v string) (tag string, commits int, dirty bool) {
	v = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(v)), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if t, ok := strings.CutSuffix(v, "-dirty"); ok {
		v, dirty = t, true
	}
	if m := describeBuild.FindStringSubmatch(v); m != nil {
		if n, err := strconv.Atoi(m[2]); err == nil {
			return m[1], n, dirty
		}
	}
	return v, 0, dirty
}

func compareReleases(a, b string) int {
	ac, ap := splitVersion(a)
	bc, bp := splitVersion(b)
	for i := 0; i < len(ac) || i < len(bc); i++ {
		var x, y int
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		if x != y {
			return cmpInt(x, y)
		}
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		xn, xerr := strconv.Atoi(as[i])
		yn, yerr := strconv.Atoi(bs[i])
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				return cmpInt(xn, yn)
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func cmpInt(x, y int) int {
	switch {
	case x > y:
		return 1
	case x < y:
		return -1
	}
	return 0
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(v)), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	if v == "" {
		return nil, pre
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, pre
		}
		out = append(out, n)
	}
	return out, pre
}

func sha256Hex(b []byte) string {
	sum := sha256Sum(b)
	return hex.EncodeToString(sum[:])
}
