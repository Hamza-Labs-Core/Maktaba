package releaseverify

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watermark is the highest release ever committed on a machine (design §3.5).
// It only goes up: a rollback to the kept previous release runs a lower version
// without lowering it, and is the only way to do so.
type Watermark struct {
	Version         string    `json:"version"`
	ChecksumsSHA256 string    `json:"checksums_sha256,omitempty"`
	SignedAt        time.Time `json:"signed_at,omitempty"`
}

// CheckMonotonic applies the watermark rules (story 35.1 AC6) to a verified
// release:
//   - version < watermark → ErrDowngrade;
//   - version == watermark with a different checksums.txt, or signed before it →
//     ErrReplay (the same version with other content);
//   - a newer version signed earlier than the watermark release on its major.minor
//     line → ErrOlderBuild (OlderBuild: an rc tagged after its release; not an alarm);
//     on a higher major.minor it is accepted (a hotfix on an older minor, story 22.5
//     EC1).
//
// An empty watermark (first install, or a machine from before story 35.1)
// accepts anything Verify accepted.
func CheckMonotonic(w Watermark, r *Result) error {
	if r == nil || r.Manifest == nil {
		return fail(ErrManifest, ManifestFile, "no verified release", nil)
	}
	if strings.TrimSpace(w.Version) == "" {
		return nil
	}
	v := r.Manifest.Version
	switch c := CompareVersions(v, w.Version); {
	case c < 0:
		return fail(ErrDowngrade, "", fmt.Sprintf("%s is older than %s, the highest version installed here (roll back to the kept previous release instead)", v, w.Version), nil)
	case c == 0 && w.ChecksumsSHA256 != "" && w.ChecksumsSHA256 != r.ChecksumsSHA256:
		return fail(ErrReplay, ChecksumsFile, fmt.Sprintf("%s was installed with different checksums (%s, now %s)", v, w.ChecksumsSHA256, r.ChecksumsSHA256), nil)
	}
	if w.SignedAt.IsZero() || r.SignedAt.IsZero() || !r.SignedAt.Before(w.SignedAt) {
		return nil
	}
	switch {
	case CompareVersions(v, w.Version) == 0:
		return fail(ErrReplay, BundleFile, fmt.Sprintf("%s was signed at %s, before the installed %s (%s)",
			v, r.SignedAt.Format(time.RFC3339), w.Version, w.SignedAt.Format(time.RFC3339)), nil)
	case OlderBuild(v, r.SignedAt, w.Version, w.SignedAt):
		return fail(ErrOlderBuild, BundleFile, fmt.Sprintf("%s was signed at %s, before the installed %s (%s): an "+
			"older build of this line, passed by", v, r.SignedAt.Format(time.RFC3339), w.Version,
			w.SignedAt.Format(time.RFC3339)), nil)
	}
	return nil // a higher major.minor signed earlier: a hotfix line moving up
}

// WatermarkFile persists a Watermark next to the install: Path holds the
// version on one line ("1.5.0\n", design §2.1) and Path+".json" the full record
// (checksums digest, signed time) used for the replay rules.
type WatermarkFile struct {
	Path string
}

// Load reads the watermark; a missing file is the zero Watermark.
func (f WatermarkFile) Load() (Watermark, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Watermark{}, nil
	}
	if err != nil {
		return Watermark{}, err
	}
	w := Watermark{Version: strings.TrimSpace(string(b))}
	if w.Version == "" {
		return Watermark{}, nil
	}
	if jb, err := os.ReadFile(f.Path + ".json"); err == nil {
		var full Watermark
		if json.Unmarshal(jb, &full) == nil && CompareVersions(full.Version, w.Version) == 0 {
			w = full
		}
	}
	return w, nil
}

// Commit raises the watermark to a release that passed Verify and
// CheckMonotonic and is now installed. It never lowers it.
func (f WatermarkFile) Commit(r *Result) error {
	if r == nil || r.Manifest == nil {
		return errors.New("releaseverify: commit without a verified release")
	}
	cur, err := f.Load()
	if err != nil {
		return err
	}
	if err := CheckMonotonic(cur, r); err != nil {
		return err
	}
	w := Watermark{Version: r.Manifest.Version, ChecksumsSHA256: r.ChecksumsSHA256, SignedAt: r.SignedAt}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	jb, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(f.Path+".json", append(jb, '\n')); err != nil {
		return err
	}
	return writeFileAtomic(f.Path, []byte(w.Version+"\n"))
}

// FlatWatermarkPath is where an install without the bin/releases layout
// (before story 35.2) keeps its watermark: next to the maktaba-server binary.
// The API's self-update and `maktaba-server update` share it.
func FlatWatermarkPath(self string) string {
	return filepath.Join(filepath.Dir(self), "maktaba-server.watermark")
}
