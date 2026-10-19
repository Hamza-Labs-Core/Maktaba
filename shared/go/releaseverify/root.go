package releaseverify

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

//go:generate go run ./cmd/update-trusted-root

// The Sigstore public-good trusted root (Fulcio CAs, Rekor and CT log keys,
// timestamp authorities), fetched through Sigstore's TUF repository by
// cmd/update-trusted-root and compiled in, so a machine with no network still
// verifies a release (story 35.1 AC8, design §3.3).
//
//go:embed trusted_root.json
var embeddedRootJSON []byte

//go:embed trusted_root.meta.json
var embeddedRootMeta []byte

// DefaultRootMaxAge is how long a trusted root stays usable without a successful
// TUF refresh. Sigstore rotates keys with months of overlap; past this age the
// root is treated as expired and verification fails closed.
const DefaultRootMaxAge = 180 * 24 * time.Hour

// RootInfo says which trusted root is in use.
type RootInfo struct {
	Source    string    // "embedded" or the cache file path
	FetchedAt time.Time // when TUF last vouched for it
}

type rootMeta struct {
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"source,omitempty"`
}

var (
	embeddedOnce sync.Once
	embeddedRoot *root.TrustedRoot
	embeddedErr  error
)

// EmbeddedTrustedRoot is the compiled-in root, without the age check (tools and
// tests; machines use a RootStore).
func EmbeddedTrustedRoot() (*root.TrustedRoot, error) {
	embeddedOnce.Do(func() {
		embeddedRoot, embeddedErr = root.NewTrustedRootFromJSON(embeddedRootJSON)
		if embeddedErr != nil {
			embeddedErr = fail(ErrSignature, "trusted_root.json", "embedded root does not parse", embeddedErr)
		}
	})
	return embeddedRoot, embeddedErr
}

// EmbeddedRootInfo reports when the embedded root was fetched.
func EmbeddedRootInfo() (RootInfo, error) {
	var m rootMeta
	if err := json.Unmarshal(embeddedRootMeta, &m); err != nil {
		return RootInfo{}, err
	}
	return RootInfo{Source: "embedded", FetchedAt: m.FetchedAt}, nil
}

// RootStore keeps the trusted root fresh: the embedded root is the starting
// point, Refresh (daily on the home, design §3.3) pulls trusted_root.json from
// Sigstore's TUF repository — anchored on sigstore-go's compiled-in TUF root and
// verified by the TUF client — into Dir, and TrustedMaterial serves the newest
// good copy. A failed refresh keeps the last good root; a root older than
// MaxAge, or TUF reporting expired metadata, is ErrRootExpired and is reported
// through OnExpired (critical notification, story 35.13). Verification never
// fails open. The cache is used only while no other user can write it (Dir and
// its ancestors owned by this user or root, not group/other-writable, files
// not symlinks) and while its fetched_at is not in the future; otherwise the
// embedded root is used and Refresh refuses the directory.
type RootStore struct {
	// Dir is <data_dir>/sigstore; "" keeps everything in memory (embedded only).
	Dir string
	// MaxAge defaults to DefaultRootMaxAge.
	MaxAge time.Duration
	// OnExpired is called when the root is found expired (may be nil).
	OnExpired func(error)
	// Now and Fetch are test seams. Fetch returns trusted_root.json bytes
	// verified by TUF; the default uses sigstore-go's TUF client with
	// cacheDir as its metadata cache.
	Now   func() time.Time
	Fetch func(ctx context.Context, cacheDir string) ([]byte, error)
}

func (s *RootStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *RootStore) maxAge() time.Duration {
	if s.MaxAge > 0 {
		return s.MaxAge
	}
	return DefaultRootMaxAge
}

// maxFutureSkew is how far in the future a cached root's fetched_at may lie
// (clock skew). Refresh stamps it with the local clock, so a later value means
// the clock was wrong when it was written or the file was edited; either way
// it would keep that root from ever ageing out, so it is ignored.
const maxFutureSkew = 24 * time.Hour

func cachePaths(dir string) (rootPath, metaPath string) {
	return filepath.Join(dir, "trusted_root.json"), filepath.Join(dir, "trusted_root.meta.json")
}

// TrustedMaterial returns the newest good trusted root, failing closed with
// ErrRootExpired when it is older than MaxAge.
func (s *RootStore) TrustedMaterial() (root.TrustedMaterial, RootInfo, error) {
	tr, info, err := s.load()
	if err != nil {
		return nil, RootInfo{}, err
	}
	if age := s.now().Sub(info.FetchedAt); age > s.maxAge() {
		e := fail(ErrRootExpired, "trusted_root.json",
			fmt.Sprintf("last refreshed %s (%s ago, limit %s); refresh it from Sigstore's TUF repository",
				info.FetchedAt.Format(time.RFC3339), age.Round(time.Hour), s.maxAge()), nil)
		s.expired(e)
		return nil, info, e
	}
	return tr, info, nil
}

func (s *RootStore) load() (*root.TrustedRoot, RootInfo, error) {
	emb, err := EmbeddedTrustedRoot()
	if err != nil {
		return nil, RootInfo{}, err
	}
	embInfo, err := EmbeddedRootInfo()
	if err != nil {
		return nil, RootInfo{}, fail(ErrSignature, "trusted_root.meta.json", "embedded metadata", err)
	}
	if s.Dir == "" {
		return emb, embInfo, nil
	}
	// The cache is only a trust anchor when no other user can write it (see
	// trustedDir); otherwise the embedded root is used.
	dir, err := trustedDir(s.Dir)
	if err != nil {
		return emb, embInfo, nil
	}
	rootPath, metaPath := cachePaths(dir)
	if trustedFile(rootPath) != nil || trustedFile(metaPath) != nil {
		return emb, embInfo, nil
	}
	raw, err1 := os.ReadFile(rootPath)
	mraw, err2 := os.ReadFile(metaPath)
	if err1 != nil || err2 != nil {
		return emb, embInfo, nil
	}
	var m rootMeta
	if json.Unmarshal(mraw, &m) != nil || !m.FetchedAt.After(embInfo.FetchedAt) ||
		m.FetchedAt.After(s.now().Add(maxFutureSkew)) {
		return emb, embInfo, nil
	}
	tr, err := root.NewTrustedRootFromJSON(raw)
	if err != nil {
		return emb, embInfo, nil // a damaged cache never replaces the embedded root
	}
	return tr, RootInfo{Source: rootPath, FetchedAt: m.FetchedAt}, nil
}

// Refresh fetches trusted_root.json through TUF and stores it in Dir. On any
// failure the previous root stays in use; TUF's expired-metadata error is
// ErrRootExpired.
func (s *RootStore) Refresh(ctx context.Context) error {
	if s.Dir == "" {
		return errors.New("releaseverify: RootStore.Dir not set")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	// A umask of 002 makes the new directory group-writable, which trustedDir
	// refuses: drop group/other write from the cache directory itself.
	if fi, err := os.Stat(s.Dir); err == nil && fi.Mode().Perm()&0o022 != 0 {
		_ = os.Chmod(s.Dir, fi.Mode().Perm()&^0o022)
	}
	dir, err := trustedDir(s.Dir)
	if err != nil {
		return fmt.Errorf("releaseverify: refusing the trusted-root cache %s: %w", s.Dir, err)
	}
	fetch := s.Fetch
	if fetch == nil {
		fetch = fetchTUF
	}
	raw, err := fetch(ctx, filepath.Join(dir, "tuf"))
	if err != nil {
		var exp *metadata.ErrExpiredMetadata
		if errors.As(err, &exp) {
			e := fail(ErrRootExpired, "trusted_root.json", "TUF metadata expired", err)
			s.expired(e)
			return e
		}
		return fmt.Errorf("releaseverify: refresh trusted root: %w", err)
	}
	if _, err := root.NewTrustedRootFromJSON(raw); err != nil {
		return fmt.Errorf("releaseverify: refreshed trusted root does not parse: %w", err)
	}
	meta, err := json.Marshal(rootMeta{FetchedAt: s.now().UTC(), Source: tuf.DefaultMirror})
	if err != nil {
		return err
	}
	rootPath, metaPath := cachePaths(dir)
	if err := writeFileAtomic(rootPath, raw); err != nil {
		return err
	}
	return writeFileAtomic(metaPath, meta)
}

func (s *RootStore) expired(err error) {
	if s.OnExpired != nil {
		s.OnExpired(err)
	}
}

func fetchTUF(ctx context.Context, cacheDir string) ([]byte, error) {
	opts := tuf.DefaultOptions().WithCachePath(cacheDir).WithContext(ctx)
	c, err := tuf.New(opts)
	if err != nil {
		return nil, err
	}
	return c.GetTarget("trusted_root.json")
}

// writeFileAtomic writes path via a temp file in the same directory, fsync and
// rename.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
