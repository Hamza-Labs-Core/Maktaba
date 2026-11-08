// Package pgbin fetches the Postgres a machine runs its copy of the home's database
// with (specs/epics/37-node-replica): the build the home bundles (Maktaba.app,
// tools/release/mac-app/pins.env), theseus-rs/postgresql-binaries 16.15.0 for this
// machine's platform. Every archive is checked against its pinned SHA-256 and size
// before it is unpacked, into <root>/postgresql-<version>-<triple>, once.
package pgbin

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ghasset"
)

// Version is the Postgres every machine runs (the home's major: a copy needs it).
const Version = "16.15.0"

// Major is Version's major.
const Major = "16"

// Pin is one platform's archive.
type Pin struct {
	Triple string
	SHA256 string
	Size   int64
	Zip    bool
}

// URL is the archive's github.com download (pins.env's POSTGRES_URL; Ensure tries
// Maktaba's CDN and GitHub's API before it).
func (p Pin) URL() string { return DefaultReleases + "/" + Version + "/" + p.Name() }

// Name is the archive's file name.
func (p Pin) Name() string {
	ext := ".tar.gz"
	if p.Zip {
		ext = ".zip"
	}
	return "postgresql-" + Version + "-" + p.Triple + ext
}

// Dir is the unpacked install's directory name.
func (p Pin) Dir() string { return "postgresql-" + Version + "-" + p.Triple }

// pins are the archives, by GOOS/GOARCH. darwin/arm64 is the one Maktaba.app ships
// (pins.env; a test keeps them equal).
var pins = map[string]Pin{
	"windows/amd64": {Triple: "x86_64-pc-windows-msvc", Zip: true, Size: 46890560,
		SHA256: "98fe43cff00f5aef4a7669adb575655e913dc65b1fdfb997c46fe3c1e07f4efb"},
	"linux/amd64": {Triple: "x86_64-unknown-linux-gnu", Size: 11087052,
		SHA256: "77dd669eda3985ea8be26256f6af28d3c8414152ada713797555bb7423b4486a"},
	"linux/arm64": {Triple: "aarch64-unknown-linux-gnu", Size: 10897360,
		SHA256: "f6d49ffbaea28cf8732785b18afb75bded905667d4ec8e2308a13a30fe0a24d1"},
	"darwin/arm64": {Triple: "aarch64-apple-darwin", Size: 12107763,
		SHA256: "46f6382024d9b633d1f4b4903ffef8c2404e00ae83098d628c00591048cb0512"},
	"darwin/amd64": {Triple: "x86_64-apple-darwin", Size: 12408962,
		SHA256: "dfae21b2b931f1d8d1103afb6e8e108fdd94c6f407ce24d35b6cd0ad34f286d1"},
}

// For is the archive of a platform.
func For(goos, goarch string) (Pin, bool) {
	p, ok := pins[goos+"/"+goarch]
	return p, ok
}

// Here is this machine's archive.
func Here() (Pin, bool) { return For(runtime.GOOS, runtime.GOARCH) }

// Exe is a program's file name on this OS.
func Exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Installed reports the install at home has the programs a copy needs (a logical
// subscription: no pg_basebackup).
func Installed(home string) bool {
	for _, b := range []string{"postgres", "initdb", "pg_ctl", "psql"} {
		if info, err := os.Stat(filepath.Join(home, "bin", Exe(b))); err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// The places an archive is downloaded from, in the order they are tried: Maktaba's CDN
// (the store's public bucket, plugins-cdn.maktaba.tv, where the release workflow
// publishes each pinned archive: .github/workflows/postgres-cdn.yml), GitHub's API asset
// route (the file on release-assets.githubusercontent.com, reached without github.com's
// download door), then that door. Whichever answers, the archive must match its pin.
const (
	DefaultCDN      = "https://plugins-cdn.maktaba.tv"
	DefaultAPI      = ghasset.DefaultAPI
	DefaultReleases = "https://github.com/" + Repo + "/releases/download"
	// Repo is the GitHub repository the archives are released in.
	Repo = "theseus-rs/postgresql-binaries"
)

// CDNPath is the archive's path on Maktaba's CDN: postgres/<version>/<name>.
func (p Pin) CDNPath() string { return "postgres/" + Version + "/" + p.Name() }

// Mirrors are the bases of the three places (tests point them at fake servers; "" leaves
// one out).
type Mirrors struct {
	CDN      string // <CDN>/postgres/<version>/<name>
	API      string // <API>/repos/<Repo>/releases/tags/<version>, then its asset route
	Releases string // <Releases>/<version>/<name>
	Repo     string // the GitHub repository (default Repo)
}

// DefaultMirrors are the real places.
func DefaultMirrors() Mirrors {
	return Mirrors{CDN: DefaultCDN, API: DefaultAPI, Releases: DefaultReleases, Repo: Repo}
}

// Source is one place an archive is downloaded from: Fetch makes one try, writing the
// archive to w.
type Source struct {
	Name  string
	Fetch func(ctx context.Context, pin Pin, w io.Writer) error
}

// Downloader tries its sources in order, each up to Tries times on a transient failure
// (a 5xx, a timeout, a connection error: ghasset.Transient), waiting Backoff, then twice
// that, between tries; any other failure (a 404, another 4xx), or an archive that does
// not match its pin, moves on to the next source at once.
type Downloader struct {
	Sources []Source
	Tries   int                                              // per source (0: 3)
	Backoff time.Duration                                    // the first wait (0: 2 s)
	Sleep   func(ctx context.Context, d time.Duration) error // tests replace it (nil: ghasset.Sleep)
}

func (d *Downloader) tries() int {
	if d.Tries > 0 {
		return d.Tries
	}
	return 3
}

func (d *Downloader) wait(ctx context.Context, try int) (time.Duration, error) {
	first := d.Backoff
	if first <= 0 {
		first = 2 * time.Second
	}
	w := ghasset.Backoff(first, try)
	sleep := d.Sleep
	if sleep == nil {
		sleep = ghasset.Sleep
	}
	return w, sleep(ctx, w)
}

// HTTPDownloader downloads over HTTP from m's places, with c (nil: ghasset.Client(), which
// honours the environment's proxy).
func HTTPDownloader(c *http.Client, m Mirrors) *Downloader {
	if c == nil {
		c = ghasset.Client()
	}
	if m.Repo == "" {
		m.Repo = Repo
	}
	d := &Downloader{}
	if b := strings.TrimRight(m.CDN, "/"); b != "" {
		d.Sources = append(d.Sources, Source{Name: "Maktaba's CDN", Fetch: func(ctx context.Context, p Pin, w io.Writer) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, b+"/"+p.CDNPath(), nil)
			if err != nil {
				return err
			}
			return get(c, req, w)
		}})
	}
	if b := strings.TrimRight(m.API, "/"); b != "" {
		d.Sources = append(d.Sources, Source{Name: "GitHub's API", Fetch: func(ctx context.Context, p Pin, w io.Writer) error {
			u, err := ghasset.AssetURL(ctx, c, b, m.Repo, Version, p.Name())
			if err != nil {
				return err
			}
			req, err := ghasset.AssetRequest(ctx, u)
			if err != nil {
				return err
			}
			return get(c, req, w)
		}})
	}
	if b := strings.TrimRight(m.Releases, "/"); b != "" {
		d.Sources = append(d.Sources, Source{Name: "github.com", Fetch: func(ctx context.Context, p Pin, w io.Writer) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, b+"/"+Version+"/"+p.Name(), nil)
			if err != nil {
				return err
			}
			return get(c, req, w)
		}})
	}
	return d
}

// stallTimeout is how long a download may go without a byte before it is given up as a
// timeout (tried again): a place that stops sending mid-archive would hold it forever.
var stallTimeout = 60 * time.Second

// get does req and copies its body to w, failing (a timeout) when no byte arrives for
// stallTimeout.
func get(c *http.Client, req *http.Request, w io.Writer) error {
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	var stalled atomic.Bool
	timer := time.AfterFunc(stallTimeout, func() { stalled.Store(true); cancel() })
	defer timer.Stop()
	err := getBody(c, req.WithContext(ctx), w, func() { timer.Reset(stallTimeout) })
	if err != nil && stalled.Load() && req.Context().Err() == nil {
		return fmt.Errorf("no data for %s: %w", stallTimeout, context.DeadlineExceeded)
	}
	return err
}

func getBody(c *http.Client, req *http.Request, w io.Writer, progress func()) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &ghasset.StatusError{Code: resp.StatusCode}
	}
	// a length said up front that w cannot take (Ensure's capped) is refused before the
	// body is read
	if c, ok := w.(*capped); ok && resp.ContentLength > c.left {
		return fmt.Errorf("%d bytes: %w", resp.ContentLength, errTooLarge)
	}
	_, err = io.Copy(w, progressReader{resp.Body, progress})
	return err
}

// progressReader calls progress after every read that got bytes.
type progressReader struct {
	r        io.Reader
	progress func()
}

func (p progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.progress()
	}
	return n, err
}

// errMismatch: an archive that does not match its pin.
var errMismatch = errors.New("does not match its pin")

// download fetches pin's archive into a new file under root (a dot file, which nothing
// takes for an install) and returns its path once it matches the pin; a source whose
// archive does not match is not tried again, and no unverified file is left.
func (d *Downloader) download(ctx context.Context, root string, pin Pin, log io.Writer) (string, error) {
	var failed sourceErrors
	for _, s := range d.Sources {
		for try := 1; ; try++ {
			p, err := d.once(ctx, root, pin, s)
			if err == nil {
				fmt.Fprintf(log, "[postgres] PostgreSQL %s downloaded from %s\n", Version, s.Name)
				return p, nil
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			retry := ghasset.Transient(err) && !errors.Is(err, errMismatch) && !errors.Is(err, errTooLarge) && try < d.tries()
			if !retry {
				fmt.Fprintf(log, "[postgres] %s failed (try %d): %v: the next source\n", s.Name, try, err)
				failed = append(failed, fmt.Errorf("%s: %w", s.Name, err))
				break
			}
			wait, werr := d.wait(ctx, try)
			fmt.Fprintf(log, "[postgres] %s failed (try %d of %d): %v: again in %s\n", s.Name, try, d.tries(), err, wait)
			if werr != nil {
				return "", werr
			}
		}
	}
	if len(failed) == 0 {
		return "", errors.New("no source to download from")
	}
	return "", failed
}

// sourceErrors are each source's last failure, in order.
type sourceErrors []error

func (e sourceErrors) Error() string {
	s := make([]string, len(e))
	for i, err := range e {
		s[i] = err.Error()
	}
	return strings.Join(s, "; ")
}

func (e sourceErrors) Unwrap() []error { return e }

// once is one try at s: the archive in a new dot file under root, its path returned only
// when its SHA-256 and size are the pin's (the file is removed otherwise).
func (d *Downloader) once(ctx context.Context, root string, pin Pin, s Source) (path string, err error) {
	tmp, err := os.CreateTemp(root, ".download-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	if err := s.Fetch(ctx, pin, &capped{w: io.MultiWriter(tmp, h), left: pin.Size}); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	info, err := tmp.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() != pin.Size {
		return "", fmt.Errorf("archive of %d bytes, pinned %d: %w", info.Size(), pin.Size, errMismatch)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != pin.SHA256 {
		return "", fmt.Errorf("archive SHA-256 %s, pinned %s: %w", sum, pin.SHA256, errMismatch)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

// errTooLarge: a download sent more than its pin's size.
var errTooLarge = errors.New("more than the pinned size")

// capped passes on at most left bytes, failing the write that would go past them:
// an archive larger than its pin is refused as it arrives, not once it is on disk.
type capped struct {
	w    io.Writer
	left int64
}

func (c *capped) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, errTooLarge
	}
	n, err := c.w.Write(p)
	c.left -= int64(n)
	return n, err
}

// outputPluginMark is what every logical decoding output plugin exports (its entry
// point), and strippedMark the file an install keeps once StripDecoders ran on it.
const (
	outputPluginMark = "_PG_output_plugin_init"
	strippedMark     = ".maktaba-pgoutput-only"
)

// OutputPlugins are the logical decoding output plugins of the install at home but
// pgoutput (its libraries' paths).
func OutputPlugins(home string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(filepath.Join(home, "lib"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		switch filepath.Ext(name) {
		case ".so", ".dylib", ".dll":
		default:
			return nil
		}
		if strings.TrimSuffix(name, filepath.Ext(name)) == "pgoutput" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(outputPluginMark)) {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// StripDecoders removes from the install at home every logical decoding output plugin
// but pgoutput, a copy's (specs/epics/37-node-replica, "What a machine can read"): a
// machine's replication role may make a slot with any plugin its home's server has, and
// another one (test_decoding) decodes every table, past what is published to it. Done
// once per install (strippedMark): the paths it removed.
func StripDecoders(home string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(home, strippedMark)); err == nil {
		return nil, nil
	}
	found, err := OutputPlugins(home)
	if err != nil {
		return nil, err
	}
	for _, p := range found {
		if err := os.Remove(p); err != nil {
			return nil, err
		}
	}
	return found, os.WriteFile(filepath.Join(home, strippedMark), nil, 0o644)
}

// Ensure returns the install of pin under root, downloading (dl: its sources in order),
// checking and unpacking it when it is not there yet (a half-done unpack, or an archive
// that does not match its pin, is never taken for an install), with no output plugin but
// pgoutput (StripDecoders: an install unpacked before that is stripped now).
func Ensure(ctx context.Context, root string, pin Pin, dl *Downloader, log io.Writer) (string, error) {
	home := filepath.Join(root, pin.Dir())
	if Installed(home) {
		if _, err := StripDecoders(home); err != nil {
			return "", fmt.Errorf("PostgreSQL %s: its output plugins: %w", Version, err)
		}
		return home, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	fmt.Fprintf(log, "[postgres] downloading PostgreSQL %s for this machine (%s, %d MB)\n", Version, pin.Triple, pin.Size>>20)
	archive, err := dl.download(ctx, root, pin, log)
	if err != nil {
		return "", fmt.Errorf("download PostgreSQL %s: %w", Version, err)
	}
	defer func() { _ = os.Remove(archive) }()
	stage, err := os.MkdirTemp(root, ".unpack-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if pin.Zip {
		err = unzip(archive, stage)
	} else {
		err = untargz(archive, stage)
	}
	if err != nil {
		return "", fmt.Errorf("unpack PostgreSQL %s: %w", Version, err)
	}
	got := filepath.Join(stage, pin.Dir())
	if !Installed(got) {
		return "", errors.New("unexpected PostgreSQL archive layout")
	}
	if _, err := StripDecoders(got); err != nil {
		return "", fmt.Errorf("PostgreSQL %s: its output plugins: %w", Version, err)
	}
	if err := os.RemoveAll(home); err != nil {
		return "", err
	}
	if err := os.Rename(got, home); err != nil {
		return "", err
	}
	return home, nil
}

// inside joins name under dir, refusing a name that is not local (absolute, a
// drive's, a reserved one on Windows) or has a ".." in it, and a path that leaves dir.
func inside(dir, name string) (string, error) {
	name = filepath.FromSlash(name)
	if !filepath.IsLocal(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("archive entry %q leaves the install", name)
	}
	root := filepath.Clean(dir) + string(filepath.Separator)
	p := filepath.Join(dir, name)
	if p != filepath.Clean(dir) && !strings.HasPrefix(p, root) {
		return "", fmt.Errorf("archive entry %q leaves the install", name)
	}
	return p, nil
}

func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p, err := inside(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(p, rc, f.Mode().Perm()|0o600)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func untargz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	// dir with its links resolved, as a link's directory is checked against it (a
	// Mac's /var is /private/var)
	realRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := inside(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := writeFile(p, tr, os.FileMode(hdr.Mode).Perm()|0o600); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// a library's versioned name: only a link to a file beside it (not
			// absolute, a drive's, "" or "..": IsLocal)
			target := filepath.Join(filepath.Dir(p), hdr.Linkname)
			if hdr.Linkname == "." || !filepath.IsLocal(hdr.Linkname) ||
				strings.ContainsAny(hdr.Linkname, `/\`) || filepath.Dir(target) != filepath.Dir(p) ||
				!strings.HasPrefix(target, filepath.Clean(dir)+string(filepath.Separator)) {
				return fmt.Errorf("archive link %q leaves its directory", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			// and with links resolved: the target's directory (the target itself
			// may come later) is not reached through a link out of the install
			parent, err := filepath.EvalSymlinks(filepath.Dir(target))
			if err != nil {
				return err
			}
			if rel, err := filepath.Rel(realRoot, parent); err != nil || (rel != "." && !filepath.IsLocal(rel)) {
				return fmt.Errorf("archive link %q leaves the install", hdr.Name)
			}
			_ = os.Remove(p)
			if err := os.Symlink(hdr.Linkname, p); err != nil {
				return err
			}
		}
	}
}

func writeFile(p string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
