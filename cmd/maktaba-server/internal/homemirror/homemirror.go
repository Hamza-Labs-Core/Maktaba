// Package homemirror is a node's way to its home's release mirror (Epic 35 story 35.12
// AC3, design §9.2 and §3.4): the release files and the pipeline's wheelhouse the home
// keeps for its machines. A node downloads a release from its home first and from GitHub
// after three failures (at once when the home does not hold the file), and builds its
// pipeline environment from the home's wheelhouse when the home keeps one: a machine
// without internet access updates all the same.
//
// Nothing here is trusted for being the home's: every release file is verified by the
// updater exactly as one from GitHub (the signed checksums, the Sigstore bundle, the
// pinned identity), and pip checks every wheel against its hash in the release's
// constraints. Requests carry the node's delegation signature (story 35.9), the same the
// node's API signs its requests to the home with (api/internal/cluster/delegation; its
// vectors are shared/testdata/cluster/delegation_vectors.json).
package homemirror

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Key is the node's delegation key (the machine agent's, from the home's keys frame).
type Key struct {
	ID     string
	Secret []byte
}

// The delegation signature's headers (api/internal/cluster/delegation).
const (
	headerNode  = "X-Maktaba-Node"
	headerKey   = "X-Maktaba-Node-Key"
	headerTs    = "X-Maktaba-Node-Ts"
	headerNonce = "X-Maktaba-Node-Nonce"
	headerSig   = "X-Maktaba-Node-Sig"
	headerVia   = "X-Maktaba-Via"
)

// emptyBodyHash is sha256("") in hex: every request here has no body.
const emptyBodyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// canonical is the signed string: method, path?query, ts, nonce, sha256(body), the
// delegated session's hash ("" here: no user is delegated).
func canonical(method, pathQuery string, ts int64, nonce, bodyHash, sessionHash string) string {
	return strings.ToUpper(method) + "\n" + pathQuery + "\n" + strconv.FormatInt(ts, 10) + "\n" + nonce + "\n" +
		bodyHash + "\n" + sessionHash
}

// signature is b64(HMAC-SHA256(key, canonical)).
func signature(key []byte, s string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// Sign adds the node's delegation signature to r, a request without a body.
func Sign(r *http.Request, nodeID string, key Key, now time.Time) error {
	if nodeID == "" || key.ID == "" || len(key.Secret) == 0 {
		return errors.New("homemirror: no delegation key")
	}
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		return err
	}
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	pq := r.URL.EscapedPath()
	if pq == "" {
		pq = "/"
	}
	if r.URL.RawQuery != "" {
		pq += "?" + r.URL.RawQuery
	}
	ts := now.Unix()
	r.Header.Set(headerNode, nodeID)
	r.Header.Set(headerKey, key.ID)
	r.Header.Set(headerTs, strconv.FormatInt(ts, 10))
	r.Header.Set(headerNonce, nonce)
	r.Header.Set(headerSig, signature(key.Secret, canonical(r.Method, pq, ts, nonce, emptyBodyHash, "")))
	r.Header.Set(headerVia, "node")
	return nil
}

// Client reaches the home's mirror.
type Client struct {
	// Home is the home's API base URL (node.json's).
	Home   string
	NodeID string
	// Key is the delegation key now (false: none yet, the agent has not reached the home).
	Key func() (Key, bool)
	// Base is the transport under the mirror's (nil: http.DefaultTransport).
	Base http.RoundTripper
	// Link carries the mirror's requests over the machine's cluster link (story 35.16):
	// tried first, unsigned (the link proves the machine); the signed path when it does
	// not answer (the link not up), which the home refuses once it requires the link.
	Link http.RoundTripper
	Now  func() time.Time
	// Log gets one line per file taken from the mirror or not (nil: none).
	Log io.Writer
	// Tries per file before GitHub (3), Pause between them (2 s), WheelWait how long a
	// wheelhouse the home is still downloading is waited for (45 min), WheelPoll how often
	// it is asked again meanwhile (0: as the home's Retry-After says, else 30 s).
	Tries     int
	Pause     time.Duration
	WheelWait time.Duration
	WheelPoll time.Duration

	mu sync.Mutex
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) base() http.RoundTripper {
	if c.Base != nil {
		return c.Base
	}
	return http.DefaultTransport
}

func (c *Client) logf(format string, args ...any) {
	if c.Log == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = fmt.Fprintf(c.Log, "[update] "+format+"\n", args...)
}

func (c *Client) tries() int {
	if c.Tries > 0 {
		return c.Tries
	}
	return 3
}

func (c *Client) pause() time.Duration {
	if c.Pause > 0 {
		return c.Pause
	}
	return 2 * time.Second
}

func (c *Client) usable() (Key, bool) {
	if c == nil || strings.TrimSpace(c.Home) == "" || c.NodeID == "" {
		return Key{}, false
	}
	if c.Key != nil {
		if k, ok := c.Key(); ok {
			return k, true
		}
	}
	// no delegation key (the link required, or not yet one): the link alone
	return Key{}, c.Link != nil
}

// overLink sends req on the machine's cluster link; false when the link does not carry
// it (not up), and the signed path is tried.
func (c *Client) overLink(req *http.Request) (*http.Response, bool) {
	if c.Link == nil {
		return nil, false
	}
	resp, err := c.Link.RoundTrip(req)
	if err != nil {
		return nil, false
	}
	if resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get("X-Maktaba-Link") == "down" {
		_ = resp.Body.Close()
		return nil, false
	}
	return resp, true
}

// sign signs req with key, or fails without one.
func (c *Client) sign(req *http.Request, key Key) error {
	if len(key.Secret) == 0 {
		return errors.New("homemirror: the cluster link is not up and there is no delegation key")
	}
	return Sign(req, c.NodeID, key, c.now())
}

// get sends a signed GET of path (under the home's API) with hdr's headers.
func (c *Client) get(ctx context.Context, path string, key Key, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Home, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	if resp, ok := c.overLink(req); ok {
		return resp, nil
	}
	if err := c.sign(req, key); err != nil {
		return nil, err
	}
	return c.base().RoundTrip(req)
}

// releaseAssetPath is a GitHub release asset's download path: /<owner>/<repo>/releases/download/<tag>/<name>.
var releaseAssetPath = regexp.MustCompile(`^/[^/]+/[^/]+/releases/download/([^/]+)/([^/]+)$`)

// releaseAsset is the version and file name of a release asset's URL.
func releaseAsset(u *url.URL) (version, name string, ok bool) {
	m := releaseAssetPath.FindStringSubmatch(u.EscapedPath())
	if m == nil {
		return "", "", false
	}
	tag, err1 := url.PathUnescape(m[1])
	name, err2 := url.PathUnescape(m[2])
	if err1 != nil || err2 != nil || name == "" || strings.ContainsAny(name, "/\\") {
		return "", "", false
	}
	return strings.TrimPrefix(tag, "v"), name, true
}

// Transport sends a release asset's download (…/releases/download/<tag>/<name>) to the
// home's mirror first, and to GitHub after Tries failures, or at once when the home does
// not hold the file (404), does not take the node's signature, or could not fetch it
// itself (502). Every other request goes to base untouched.
func (c *Client) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{c: c, base: base}
}

type transport struct {
	c    *Client
	base http.RoundTripper
}

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	version, name, ok := releaseAsset(r.URL)
	if !ok || r.Method != http.MethodGet {
		return t.base.RoundTrip(r)
	}
	key, ok := t.c.usable()
	if !ok {
		return t.base.RoundTrip(r)
	}
	path := "/api/cluster/updates/artifacts/" + url.PathEscape(version) + "/" + url.PathEscape(name)
	hdr := http.Header{}
	for _, h := range []string{"Range", "If-Range", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			hdr.Set(h, v)
		}
	}
	why := ""
	for try := 0; try < t.c.tries(); try++ {
		resp, err := t.c.getVia(r.Context(), t.base, path, key, hdr)
		if err == nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
			t.c.logf("%s from the home's mirror", name)
			return resp, nil
		}
		if r.Context().Err() != nil {
			if resp != nil {
				_ = resp.Body.Close()
			}
			return nil, r.Context().Err()
		}
		wait := t.c.pause()
		if err != nil {
			why = err.Error()
		} else {
			why = "HTTP " + strconv.Itoa(resp.StatusCode)
			if s := resp.Header.Get("Retry-After"); s != "" {
				if n, err := strconv.Atoi(s); err == nil && n > 0 && time.Duration(n)*time.Second < 30*time.Second {
					wait = time.Duration(n) * time.Second
				}
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized ||
				resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadGateway {
				// the home does not hold it, does not take this node's word, or could not get
				// it from GitHub itself: GitHub
				break
			}
		}
		if try+1 < t.c.tries() {
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(wait):
			}
		}
	}
	t.c.logf("%s from GitHub (the home's mirror: %s)", name, why)
	return t.base.RoundTrip(r)
}

// getVia is get over a given transport (the mirror's own requests skip the mirror).
func (c *Client) getVia(ctx context.Context, rt http.RoundTripper, path string, key Key, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Home, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	if resp, ok := c.overLink(req); ok {
		return resp, nil
	}
	if err := c.sign(req, key); err != nil {
		return nil, err
	}
	return rt.RoundTrip(req)
}

// SignedTransport signs every request it carries with the node's delegation key (the
// home's pass-through of Sigstore's TUF repository, for the trusted root, goes over it);
// with no key yet it carries none.
func (c *Client) SignedTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &signed{c: c, base: base}
}

type signed struct {
	c    *Client
	base http.RoundTripper
}

func (s *signed) RoundTrip(r *http.Request) (*http.Response, error) {
	key, ok := s.c.usable()
	if !ok {
		return nil, errors.New("homemirror: no delegation key yet (the machine agent has not reached the home)")
	}
	r = r.Clone(r.Context())
	if resp, ok := s.c.overLink(r); ok {
		return resp, nil
	}
	if err := s.c.sign(r, key); err != nil {
		return nil, err
	}
	return s.base.RoundTrip(r)
}

// WheelIndex is the home's index of one wheelhouse (api/internal/updates.WheelIndex).
type WheelIndex struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Python   string `json:"python"`
	Files    []struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"files"`
	Missing []string `json:"missing,omitempty"`
}

// IndexFile is a fetched wheelhouse's index, written last (appvenv takes a directory that
// has one as complete).
const IndexFile = "index.json"

// wheelName is a wheel file's name as the home lists it.
var wheelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*\.whl$`)

// Wheelhouse fetches the home's wheelhouse of version for platform (release.json's
// "os/arch") into dir, its index last; false when the home keeps none (an older home, the
// rules' wheelhouse off, a platform it could not download for, no delegation key yet): the
// environment comes from PyPI then. A wheelhouse the home is still downloading is waited
// for (WheelWait at most).
func (c *Client) Wheelhouse(ctx context.Context, version, platform, dir string) (bool, error) {
	key, ok := c.usable()
	if !ok {
		return false, nil
	}
	seg := strings.ReplaceAll(platform, "/", "-")
	base := "/api/cluster/updates/wheelhouse/" + url.PathEscape(strings.TrimPrefix(version, "v")) + "/" + url.PathEscape(seg)
	wait := c.WheelWait
	if wait <= 0 {
		wait = 45 * time.Minute
	}
	deadline := c.now().Add(wait)
	var idx WheelIndex
	for {
		resp, err := c.get(ctx, base, key, nil)
		if err != nil {
			return false, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			if err := json.Unmarshal(body, &idx); err != nil {
				return false, fmt.Errorf("the home's wheelhouse index: %w", err)
			}
		case http.StatusAccepted, http.StatusTooManyRequests:
			if !c.now().Before(deadline) {
				return false, fmt.Errorf("the home was still downloading the wheelhouse after %s", wait)
			}
			pause := 30 * time.Second
			if s := resp.Header.Get("Retry-After"); s != "" {
				if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 120 {
					pause = time.Duration(n) * time.Second
				}
			}
			if c.WheelPoll > 0 {
				pause = c.WheelPoll
			}
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(pause):
			}
			continue
		default:
			return false, nil
		}
		break
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	_ = os.Remove(filepath.Join(dir, IndexFile))
	for _, f := range idx.Files {
		if !wheelName.MatchString(f.Name) {
			return false, fmt.Errorf("the home's wheelhouse lists %q", f.Name)
		}
		dst := filepath.Join(dir, f.Name)
		if sum, err := fileSHA256(dst); err == nil && sum == f.SHA256 {
			continue
		}
		if err := c.fetchWheel(ctx, base+"/"+url.PathEscape(f.Name), key, dst, f.SHA256, f.Size); err != nil {
			return false, err
		}
	}
	b, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(dir, IndexFile), b, 0o644); err != nil {
		return false, err
	}
	c.logf("the pipeline's packages from the home's wheelhouse (%d wheels; %d from PyPI)", len(idx.Files), len(idx.Missing))
	return true, nil
}

// fetchWheel downloads one wheel into dst and keeps it when its digest and size are the
// index's (pip checks it against the release's constraints too).
func (c *Client) fetchWheel(ctx context.Context, path string, key Key, dst, sum string, size int64) error {
	var resp *http.Response
	for try := 0; ; try++ {
		var err error
		if resp, err = c.get(ctx, path, key, nil); err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		_ = resp.Body.Close()
		// a home busy with other machines' downloads says when to come back
		if (resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable) || try >= 5 {
			return fmt.Errorf("%s: HTTP %d", filepath.Base(dst), resp.StatusCode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.pause() * time.Duration(try+1)):
		}
	}
	defer func() { _ = resp.Body.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); n != size || got != sum {
		return fmt.Errorf("%s from the home is not the wheel its index lists", filepath.Base(dst))
	}
	return os.Rename(tmp.Name(), dst)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LocalLink is the machine's cluster link as its supervisor reaches it: the node API's
// loopback fetch (GET /api/cluster/link/local/fetch?purpose=&path=, the local token),
// which opens a data stream to the home for one file (story 35.16). A link that is not
// up answers 503 with X-Maktaba-Link: down.
type LocalLink struct {
	// API is the node API on loopback (http://127.0.0.1:<port>).
	API       string
	TokenFile string
	Client    *http.Client
}

// RoundTrip implements http.RoundTripper for a GET of a path under the home's API.
func (l *LocalLink) RoundTrip(r *http.Request) (*http.Response, error) {
	purpose := "artifact"
	if strings.HasPrefix(r.URL.Path, "/api/cluster/updates/wheelhouse/") {
		purpose = "wheelhouse"
	}
	tok, err := os.ReadFile(l.TokenFile) //nolint:gosec // this machine's own token file
	if err != nil {
		return nil, err
	}
	q := url.Values{"purpose": {purpose}, "path": {r.URL.RequestURI()}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		strings.TrimRight(l.API, "/")+"/api/cluster/link/local/fetch?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	for _, h := range []string{"Range", "If-Range", "Accept", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set("X-Maktaba-Local-Token", strings.TrimSpace(string(tok)))
	c := l.Client
	if c == nil {
		c = http.DefaultClient
	}
	return c.Do(req)
}
