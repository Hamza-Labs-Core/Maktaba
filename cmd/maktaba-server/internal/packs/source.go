package packs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Signed is a release's three signed files and its component index (Index nil when the
// release has none).
type Signed struct {
	Checksums, Bundle, Manifest, Index []byte
}

// ModelFile names one file of a pinned model.
type ModelFile struct {
	ID   string // the component (model.whisper-turbo)
	Repo string // org/name on Hugging Face
	Rev  string // the 40-hex commit
	Path string // the file's path in the repo
	// Ollama: a language model's file (Repo its name, library/qwen2.5; Rev the pinned
	// manifest digest; Path blobs/sha256-<hex>), from Ollama's registry
	Ollama bool
}

// Source is where packs and models come from: the network (the release's files on
// GitHub, Hugging Face at a pinned revision) or a folder (an offline file's contents).
// Whatever it gives is checked against the signed index before it is used.
type Source interface {
	// Kind is "network" or "folder", for the log.
	Kind() string
	// Signed fetches the release's signed files for version.
	Signed(ctx context.Context, version string) (Signed, error)
	// Pack writes the release file name to dst, going on from what dst already holds
	// (a download cut short), never past size bytes; progress gets the bytes held so far.
	Pack(ctx context.Context, version, name, dst string, size int64, progress func(int64)) error
	// Model writes one model file to dst the same way.
	Model(ctx context.Context, f ModelFile, dst string, size int64, progress func(int64)) error
}

// smallLimit caps the signed files and the index.
const smallLimit = 16 << 20

// errNotFound is a file the source does not hold (HTTP 404).
var errNotFound = errors.New("not found")

// Net is the network: the release's files at <Releases>/v<version>/<name> (GitHub's
// release download URL), models at <HF>/<repo>/resolve/<revision>/<path>.
type Net struct {
	HTTP *http.Client
	// Releases is the release download base (default https://github.com/<repo>/releases/download).
	Releases string
	// HF is Hugging Face's base (default https://huggingface.co).
	HF string
	// OllamaRegistry is Ollama's model registry (default https://registry.ollama.ai).
	OllamaRegistry string
	// Token is the Hugging Face token for a gated model ("" none).
	Token func() string
	// URLs, when set, are the release's files by name (an update's job: the asset URLs
	// the home's catalog keeps, story 51.18): a name not in it is not found, and Releases
	// is not used.
	URLs map[string]string
}

// NewNet is the network source of repo ("owner/name") with c (nil: a client with no
// overall timeout: a pack is gigabytes).
func NewNet(repo string, c *http.Client) *Net {
	if c == nil {
		c = &http.Client{Transport: http.DefaultTransport}
	}
	return &Net{HTTP: c, Releases: "https://github.com/" + repo + "/releases/download", HF: "https://huggingface.co"}
}

func (n *Net) Kind() string { return "network" }

func (n *Net) releaseURL(version, name string) string {
	if n.URLs != nil {
		return n.URLs[name]
	}
	return strings.TrimRight(n.Releases, "/") + "/v" + url.PathEscape(version) + "/" + url.PathEscape(name)
}

func (n *Net) Signed(ctx context.Context, version string) (Signed, error) {
	var s Signed
	get := func(name string, optional bool) ([]byte, error) {
		b, err := n.small(ctx, n.releaseURL(version, name))
		if optional && errors.Is(err, errNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return b, nil
	}
	var err error
	if s.Checksums, err = get(rv.ChecksumsFile, false); err != nil {
		return s, err
	}
	if s.Bundle, err = get(rv.BundleFile, false); err != nil {
		return s, err
	}
	if s.Manifest, err = get(rv.ManifestFile, false); err != nil {
		return s, err
	}
	s.Index, err = get(rv.ComponentsIndexName(version), true)
	return s, err
}

func (n *Net) small(ctx context.Context, u string) ([]byte, error) {
	if u == "" {
		return nil, errNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	return n.fetchSmall(req)
}

// fetchSmall does req and reads its body, at most smallLimit bytes.
func (n *Net) fetchSmall(req *http.Request) ([]byte, error) {
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, smallLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > smallLimit {
		return nil, fmt.Errorf("larger than %d bytes", smallLimit)
	}
	return b, nil
}

func (n *Net) Pack(ctx context.Context, version, name, dst string, size int64, progress func(int64)) error {
	return n.resume(ctx, n.releaseURL(version, name), "", dst, size, progress)
}

func (n *Net) Model(ctx context.Context, f ModelFile, dst string, size int64, progress func(int64)) error {
	if f.Ollama {
		return n.resume(ctx, n.ollamaURL(f), "", dst, size, progress)
	}
	var segs []string
	for _, s := range strings.Split(f.Path, "/") {
		segs = append(segs, url.PathEscape(s))
	}
	u := strings.TrimRight(n.HF, "/") + "/" + f.Repo + "/resolve/" + f.Rev + "/" + strings.Join(segs, "/")
	tok := ""
	if n.Token != nil {
		tok = n.Token()
	}
	return n.resume(ctx, u, tok, dst, size, progress)
}

// resume downloads u to dst from the bytes dst holds (Range), stopping at size.
func (n *Net) resume(ctx context.Context, u, token, dst string, size int64, progress func(int64)) error {
	have := int64(0)
	if info, err := os.Stat(dst); err == nil {
		have = info.Size()
	}
	if have > size {
		if err := os.Truncate(dst, 0); err != nil {
			return err
		}
		have = 0
	}
	if have == size {
		progress(have)
		return nil
	}
	if u == "" {
		return errNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		flags |= os.O_TRUNC
		have = 0
	case http.StatusRequestedRangeNotSatisfiable:
		// what dst holds is not a start of the file: from the beginning next time
		_ = os.Truncate(dst, 0)
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	case http.StatusNotFound:
		return errNotFound
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, flags, 0o644)
	if err != nil {
		return err
	}
	_, err = copyProgress(ctx, f, io.LimitReader(resp.Body, size-have), have, progress)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// copyProgress copies r to w, calling progress with start plus the bytes copied (at most
// every 250 ms, and at the end).
func copyProgress(ctx context.Context, w io.Writer, r io.Reader, start int64, progress func(int64)) (int64, error) {
	buf := make([]byte, 1<<20)
	done := start
	last := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return done - start, err
		}
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return done - start, werr
			}
			done += int64(n)
			if now := time.Now(); now.Sub(last) >= 250*time.Millisecond {
				last = now
				progress(done)
			}
		}
		if errors.Is(err, io.EOF) {
			progress(done)
			return done - start, nil
		}
		if err != nil {
			return done - start, err
		}
	}
}

// FolderSource is an offline file's contents in a folder (offline.go's layout): the signed
// files at the top, packs/<name>, models/<id>/<path>.
type FolderSource struct {
	Dir string
}

func (f *FolderSource) Kind() string { return "folder" }

func (f *FolderSource) Signed(_ context.Context, version string) (Signed, error) {
	var s Signed
	read := func(name string, optional bool) ([]byte, error) {
		// a file of the folder itself: the index's name carries the version asked for
		if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("%q: not a file of the release's folder", name)
		}
		b, err := readSmall(filepath.Join(f.Dir, name))
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	var err error
	if s.Checksums, err = read(rv.ChecksumsFile, false); err != nil {
		return s, err
	}
	if s.Bundle, err = read(rv.BundleFile, false); err != nil {
		return s, err
	}
	if s.Manifest, err = read(rv.ManifestFile, false); err != nil {
		return s, err
	}
	s.Index, err = read(rv.ComponentsIndexName(version), true)
	return s, err
}

func readSmall(p string) ([]byte, error) {
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	b, err := io.ReadAll(io.LimitReader(fh, smallLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > smallLimit {
		return nil, fmt.Errorf("%s: larger than %d bytes", p, smallLimit)
	}
	return b, nil
}

func (f *FolderSource) Pack(ctx context.Context, _ string, name, dst string, size int64, progress func(int64)) error {
	return copyIn(ctx, filepath.Join(f.Dir, "packs", filepath.Base(name)), dst, size, progress)
}

func (f *FolderSource) Model(ctx context.Context, m ModelFile, dst string, size int64, progress func(int64)) error {
	if !safeRel(m.Path) || !safeRel(m.ID) {
		return fmt.Errorf("bad model path %q", m.Path)
	}
	return copyIn(ctx, filepath.Join(f.Dir, "models", m.ID, filepath.FromSlash(m.Path)), dst, size, progress)
}

// copyIn copies src to dst, never past size bytes (a longer file fails the size check).
func copyIn(ctx context.Context, src, dst string, size int64, progress func(int64)) error {
	in, err := os.Open(src)
	if errors.Is(err, os.ErrNotExist) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = copyProgress(ctx, out, io.LimitReader(in, size), 0, progress)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// safeRel: a relative "/" path with no "..", "." or empty element, no backslash, colon
// or control character.
func safeRel(p string) bool {
	if p == "" || path.IsAbs(p) || strings.ContainsAny(p, "\\:\x00") {
		return false
	}
	for _, el := range strings.Split(p, "/") {
		if el == "" || el == "." || el == ".." {
			return false
		}
		for _, r := range el {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	return true
}
