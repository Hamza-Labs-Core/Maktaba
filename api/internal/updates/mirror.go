package updates

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates/dltoken"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Mirror is the home's copy of the release files its machines install (story 35.12 AC3,
// design §9.2, §3.4):
//
//	GET /api/cluster/updates/artifacts/{version}/{name}           a file of a verified release
//	GET /api/cluster/updates/wheelhouse/{version}/{platform}      the wheelhouse's index (JSON)
//	GET /api/cluster/updates/wheelhouse/{version}/{platform}/{f}  one of its wheels
//	GET /api/cluster/updates/tuf/{file}                          Sigstore's TUF repository (tufmirror.go)
//
// Only a machine's signed request (its delegation key, story 35.9) is answered, and on the
// artifact route a worker-only machine's download token for the release it converts to
// (DownloadToken: it has no delegation key yet, story 35.8 AC8). A machine
// downloads from here first and from GitHub after three failures, and verifies every file
// itself as before: the home's copy is a source, never a trust anchor (plan D4).
//
// A file is fetched once from the release's GitHub assets, on the first machine's request
// or ahead of the machines' prepare (Prefetch), and kept only when it verifies:
// checksums.txt must have the digest whose signature the catalog verified, and every other
// file the digest checksums.txt lists for it. The Sigstore bundle (the signature itself,
// listed nowhere) is kept as published; each machine checks it with the rest. A name the
// release does not list is a 404.
//
// The wheelhouse is each release's pipeline dependencies for one platform its machines run
// (rules.updates wheelhouse, design §3.4): downloaded by the home with pip once per release
// and platform, every file pinned by its hash in the release's constraints, so a machine
// that cannot reach PyPI builds its environment from it (and the source of a file never
// matters: pip checks each one's hash).
type Mirror struct {
	Store *Store
	// Dir holds the copies: <Dir>/<version>/<name>, and <Dir>/<version>/wheelhouse/<platform>/.
	Dir  string
	HTTP *http.Client
	Log  *slog.Logger
	// Python is the interpreter the wheelhouse is downloaded with (the pipeline's: the
	// supervisor names it in MAKTABA_WHEELHOUSE_PYTHON); nil or "" builds none.
	Python func() string
	// Enabled is rules.updates wheelhouse (nil: on).
	Enabled func(ctx context.Context) bool
	// TUFUpstream is the TUF repository passed on to the machines (DefaultTUFUpstream).
	TUFUpstream string
	// FreeDisk is the free space of the disk holding a path (nil: system.DiskFreeBytes):
	// the mirror keeps nothing that would leave the home's disk without room.
	FreeDisk func(path string) (uint64, error)
	Now      func() time.Time

	mu           sync.Mutex
	tokenKey     []byte // the download tokens' key (<Dir>/.download-token.key)
	fetching     map[string]*flight
	perNode      map[string]int
	wheels       map[string]*wheelBuild
	tufTimestamp *tufCached
}

// flight is one file being fetched; every request for it waits for the same fetch.
type flight struct {
	done chan struct{}
	err  error
}

// mirrorPerNode is how many downloads one machine may have open at once (its prepare
// fetches one file at a time; the cap keeps one machine from taking the home's link).
const mirrorPerNode = 3

// mirrorFileLimit is the largest file the mirror keeps (an app payload is about 1 GB).
const mirrorFileLimit int64 = 4 << 30

// errNotMirrored: the release or the file is not one the mirror serves (a 404).
var errNotMirrored = errors.New("not mirrored")

func (m *Mirror) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

func (m *Mirror) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// mirrorRoomLeft is the room the mirror always leaves on the home's disk.
const mirrorRoomLeft uint64 = 2 << 30

// room says whether the disk holding the mirror has need bytes free besides
// mirrorRoomLeft (a disk whose free space cannot be read has).
func (m *Mirror) room(need uint64) error {
	free := m.FreeDisk
	if free == nil {
		free = system.DiskFreeBytes
	}
	dir := m.Dir
	for dir != "" {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	have, err := free(dir)
	if err != nil || have == 0 {
		return nil
	}
	if have < need+mirrorRoomLeft {
		return fmt.Errorf("the home's disk has %.1f GiB free, the mirror needs %.1f GiB more", float64(have)/(1<<30),
			float64(need+mirrorRoomLeft)/(1<<30))
	}
	return nil
}

func (m *Mirror) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

// Mount attaches the mirror's routes (a machine's signed requests only).
func (m *Mirror) Mount(r chi.Router) {
	if m == nil {
		return
	}
	r.Get("/api/cluster/updates/artifacts/{version}/{name}", m.serveArtifact)
	r.Get("/api/cluster/updates/wheelhouse/{version}/{platform}", m.serveWheelIndex)
	r.Get("/api/cluster/updates/wheelhouse/{version}/{platform}/{file}", m.serveWheel)
	r.Get("/api/cluster/updates/tuf/*", m.serveTUF)
}

// machine admits a machine's signed request (the home's delegation middleware verified
// it), within its share of downloads; release is called when the request ends. A request
// that is not a machine's is refused.
func (m *Mirror) machine(w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	node := delegation.NodeFrom(r.Context())
	if node == "" {
		machineOnly(w, r, "the release mirror answers the cluster's machines only")
		return nil, false
	}
	return m.admit(w, r, node)
}

func machineOnly(w http.ResponseWriter, r *http.Request, detail string) {
	httperror.Write(w, r, &httperror.Error{Type: "about:blank#machine_only", Title: "unauthorized",
		Status: http.StatusUnauthorized, Detail: detail})
}

// downloader admits, on the artifact route, a machine's signed request or a download
// token for version (a worker-only machine converting: story 35.8 AC8): its signature and
// expiry hold, it names version, and its machine is still one of the cluster's.
func (m *Mirror) downloader(w http.ResponseWriter, r *http.Request, version string) (release func(), ok bool) {
	if delegation.NodeFrom(r.Context()) != "" {
		return m.machine(w, r)
	}
	tok := dltoken.FromRequest(r)
	if tok == "" {
		machineOnly(w, r, "the release mirror answers the cluster's machines only")
		return nil, false
	}
	key, err := m.key()
	if err != nil {
		machineOnly(w, r, "this home issues no download tokens")
		return nil, false
	}
	c, err := dltoken.Verify(key, tok, m.now())
	if err != nil {
		machineOnly(w, r, "the download token is not valid: "+err.Error())
		return nil, false
	}
	if NormalizeVersion(c.Version) != NormalizeVersion(version) {
		machineOnly(w, r, "the download token is for another release")
		return nil, false
	}
	if !m.member(r.Context(), c.Node) {
		machineOnly(w, r, "the download token's machine is not one of the cluster's")
		return nil, false
	}
	return m.admit(w, r, c.Node)
}

// member reports whether a machine is an approved member of the cluster (a revoked or
// removed one's token stops working).
func (m *Mirror) member(ctx context.Context, node string) bool {
	if m.Store == nil || m.Store.DB == nil {
		return false
	}
	var state string
	err := m.Store.DB.QueryRowContext(ctx, `SELECT state FROM cluster_nodes WHERE id = $1`, node).Scan(&state)
	return err == nil && state == "approved"
}

// tokenKeyFile is where the download tokens' key is kept, in the mirror's folder (a file,
// never served: the copies are in the releases' folders).
const tokenKeyFile = ".download-token.key"

// key is the download tokens' key: 32 random bytes made once and kept (0600), so tokens
// outlive an API restart.
func (m *Mirror) key() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tokenKey) > 0 {
		return m.tokenKey, nil
	}
	if m.Dir == "" {
		return nil, errors.New("the mirror keeps no files")
	}
	path := filepath.Join(m.Dir, tokenKeyFile)
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		m.tokenKey = b
		return b, nil
	}
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	m.tokenKey = b
	return b, nil
}

// DownloadToken is a token that lets node fetch version's files from the artifact route
// for dltoken.Lifetime (a worker-only machine converting has no delegation key yet).
func (m *Mirror) DownloadToken(node, version string) (string, error) {
	if m == nil {
		return "", errors.New("no mirror")
	}
	key, err := m.key()
	if err != nil {
		return "", err
	}
	return dltoken.Mint(key, node, NormalizeVersion(version), m.now().Add(dltoken.Lifetime)), nil
}

// admit counts one more download of node's within its share; release is called when the
// request ends.
func (m *Mirror) admit(w http.ResponseWriter, r *http.Request, node string) (release func(), ok bool) {
	m.mu.Lock()
	if m.perNode == nil {
		m.perNode = map[string]int{}
	}
	if m.perNode[node] >= mirrorPerNode {
		m.mu.Unlock()
		w.Header().Set("Retry-After", "5")
		httperror.Write(w, r, &httperror.Error{Type: "about:blank#rate_limited", Title: "too many requests",
			Status: http.StatusTooManyRequests, Detail: fmt.Sprintf("%d downloads at once per machine", mirrorPerNode)})
		return nil, false
	}
	m.perNode[node]++
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		m.perNode[node]--
		if m.perNode[node] <= 0 {
			delete(m.perNode, node)
		}
		m.mu.Unlock()
	}, true
}

// mirrorName: a release file's name as checksums.txt may list it (no path).
var mirrorName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

func (m *Mirror) serveArtifact(w http.ResponseWriter, r *http.Request) {
	version, name := chi.URLParam(r, "version"), chi.URLParam(r, "name")
	done, ok := m.downloader(w, r, version)
	if !ok {
		return
	}
	defer done()
	path, sum, err := m.Artifact(r.Context(), version, name)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	serveFile(w, r, path, name, sum)
}

// fail answers a request the mirror cannot serve: a 404 for what it does not hold, a 502
// when GitHub would not give it the file (the machine falls back to GitHub itself).
func (m *Mirror) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNotMirrored) {
		httperror.Write(w, r, httperror.NotFound(err.Error()))
		return
	}
	if r.Context().Err() == nil {
		m.log().Warn("update mirror: a file could not be fetched", "event", "update_mirror_failed",
			"path", r.URL.Path, "err", err.Error())
	}
	httperror.Write(w, r, httperror.BadGateway(err.Error()))
}

// serveFile serves a kept file with range requests (http.ServeContent); its ETag is its
// SHA-256.
func serveFile(w http.ResponseWriter, r *http.Request, path, name, sum string) {
	f, err := os.Open(path)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound(name))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		httperror.Write(w, r, httperror.Internal(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if sum != "" {
		w.Header().Set("ETag", `"`+sum+`"`)
	}
	w.Header().Set("Cache-Control", "private, no-transform")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// release is a verified release the mirror serves (withdrawn and unverified ones are
// not).
func (m *Mirror) release(ctx context.Context, version string) (Release, error) {
	if m.Store == nil || !ValidVersion(version) {
		return Release{}, fmt.Errorf("%w: %s", errNotMirrored, version)
	}
	rel, err := m.Store.Get(ctx, version)
	if err != nil {
		return Release{}, fmt.Errorf("%w: the release %s is not in the catalog", errNotMirrored, version)
	}
	if rel.VerifyState != VerifyVerified || rel.WithdrawnAt != nil || rel.ChecksumsSHA256 == "" {
		return Release{}, fmt.Errorf("%w: the release %s is not verified", errNotMirrored, version)
	}
	return rel, nil
}

// Artifact is the kept copy of a release file (its path and SHA-256), fetched and checked
// first when the mirror does not hold it yet.
func (m *Mirror) Artifact(ctx context.Context, version, name string) (path, sum string, err error) {
	if !mirrorName.MatchString(name) {
		return "", "", fmt.Errorf("%w: %q", errNotMirrored, name)
	}
	rel, err := m.release(ctx, version)
	if err != nil {
		return "", "", err
	}
	// checksums.txt first: the digest whose signature the catalog verified
	sumsPath, err := m.ensure(ctx, rel, ChecksumsFile, rel.ChecksumsSHA256)
	if err != nil {
		return "", "", err
	}
	if name == ChecksumsFile {
		return sumsPath, rel.ChecksumsSHA256, nil
	}
	if name == BundleFile {
		p, err := m.ensure(ctx, rel, name, "")
		return p, "", err
	}
	sums, err := m.sums(rel)
	if err != nil {
		return "", "", err
	}
	want, ok := sums[name]
	if !ok {
		return "", "", fmt.Errorf("%w: %s lists no %s", errNotMirrored, rel.Tag, name)
	}
	p, err := m.ensure(ctx, rel, name, want)
	return p, want, err
}

// sums is a kept release's checksums.txt.
func (m *Mirror) sums(rel Release) (releaseverify.Checksums, error) {
	b, err := os.ReadFile(filepath.Join(m.Dir, rel.Version, ChecksumsFile))
	if err != nil {
		return nil, err
	}
	return releaseverify.ParseChecksums(b)
}

// manifest is a kept release's release.json (fetched first when needed).
func (m *Mirror) manifest(ctx context.Context, rel Release) (*releaseverify.Manifest, error) {
	p, _, err := m.Artifact(ctx, rel.Version, ManifestFile)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return releaseverify.ParseManifest(b)
}

// okSuffix marks a kept file that verified: <name>.ok holds its SHA-256 ("" for the
// bundle, which nothing lists). A file without one is fetched anew.
const okSuffix = ".ok"

// ensure is the path of a kept release file, fetched from its GitHub asset first when the
// mirror does not hold it; want is its SHA-256 ("" not checked: the bundle).
func (m *Mirror) ensure(ctx context.Context, rel Release, name, want string) (string, error) {
	dir := filepath.Join(m.Dir, rel.Version)
	path := filepath.Join(dir, name)
	if ok, err := os.ReadFile(path + okSuffix); err == nil && strings.TrimSpace(string(ok)) == want {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	m.mu.Lock()
	if m.fetching == nil {
		m.fetching = map[string]*flight{}
	}
	if f, ok := m.fetching[path]; ok {
		m.mu.Unlock()
		select {
		case <-f.done:
			return path, f.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	m.fetching[path] = f
	m.mu.Unlock()
	// the fetch outlives the request that started it (the other machines wait for it)
	go func() {
		f.err = m.fetch(context.WithoutCancel(ctx), rel, name, want, dir, path)
		m.mu.Lock()
		delete(m.fetching, path)
		m.mu.Unlock()
		close(f.done)
	}()
	select {
	case <-f.done:
		return path, f.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// fetch downloads one release file into dir and keeps it when its digest is want.
func (m *Mirror) fetch(ctx context.Context, rel Release, name, want, dir, path string) error {
	u := assetURL(rel, name)
	if u == "" {
		return fmt.Errorf("%w: %s publishes no %s", errNotMirrored, rel.Tag, name)
	}
	var size uint64
	for _, a := range rel.Assets {
		if a.Name == name && a.Size > 0 {
			size = uint64(a.Size)
		}
	}
	if err := m.room(size); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_ = os.Remove(path + okSuffix)
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := m.client().Do(req)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_ = tmp.Close()
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, mirrorFileLimit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	if n > mirrorFileLimit {
		return fmt.Errorf("GET %s: larger than %d bytes", u, mirrorFileLimit)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if want != "" && got != want {
		m.log().Error("update mirror: a release file does not match its checksum", "event", "update_mirror_mismatch",
			"version", rel.Version, "file", name, "want", want, "got", got)
		return fmt.Errorf("%s of %s has SHA-256 %s, not the %s its signed checksums list", name, rel.Tag, got, want)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if err := os.WriteFile(path+okSuffix, []byte(want), 0o644); err != nil {
		return err
	}
	m.log().Info("update mirror: release file kept", "event", "update_mirror_kept", "version", rel.Version,
		"file", name, "bytes", n)
	return nil
}

// platformFiles are the files a machine of platform (release.json's "os/arch") installs
// besides the signed small ones: its server archive, the pipeline wheel and constraints
// (and on Windows the CUDA constraints).
func platformFiles(man *releaseverify.Manifest, platform string) []string {
	var out []string
	if a, ok := man.Assets[platform]; ok && a.Archive != "" {
		out = append(out, a.Archive)
	}
	if p := man.Pipeline; p != nil {
		out = append(out, p.Wheel, p.Constraints)
		if strings.HasPrefix(platform, "windows/") && p.ConstraintsCUDA != "" {
			out = append(out, p.ConstraintsCUDA)
		}
	}
	return out
}

// Prefetch fetches, in the background, what the machines of platforms will ask for a
// release (the signed small files, each platform's files) and starts each platform's
// wheelhouse, so a machine's prepare finds them ready. Failures are logged: a machine
// falls back to GitHub and PyPI by itself.
func (m *Mirror) Prefetch(ctx context.Context, version string, platforms []string) {
	if m == nil {
		return
	}
	go func() {
		ctx := context.WithoutCancel(ctx)
		rel, err := m.release(ctx, version)
		if err != nil {
			return
		}
		for _, n := range []string{ChecksumsFile, BundleFile, ManifestFile} {
			if _, _, err := m.Artifact(ctx, rel.Version, n); err != nil {
				m.log().Warn("update mirror: prefetch failed", "event", "update_mirror_failed", "version", rel.Version,
					"file", n, "err", err.Error())
				return
			}
		}
		man, err := m.manifest(ctx, rel)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, p := range platforms {
			for _, n := range platformFiles(man, p) {
				if n == "" || seen[n] {
					continue
				}
				seen[n] = true
				if _, _, err := m.Artifact(ctx, rel.Version, n); err != nil {
					m.log().Warn("update mirror: prefetch failed", "event", "update_mirror_failed",
						"version", rel.Version, "file", n, "err", err.Error())
				}
			}
			m.startWheelhouse(ctx, rel, man, p)
		}
	}()
}

// Prune deletes the copies of every release but keep (the releases the cluster runs or
// may still install), and anything left half-written.
func (m *Mirror) Prune(keep []string) {
	if m == nil || m.Dir == "" {
		return
	}
	want := map[string]bool{}
	for _, v := range keep {
		if v = NormalizeVersion(v); v != "" {
			want[v] = true
		}
	}
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range entries {
		if !e.IsDir() || want[e.Name()] || !ValidVersion(e.Name()) {
			continue // a kept release, or not a release's (the TUF repository's files)
		}
		busy := false
		for p := range m.fetching {
			busy = busy || strings.HasPrefix(p, filepath.Join(m.Dir, e.Name())+string(filepath.Separator))
		}
		for k, b := range m.wheels {
			busy = busy || (strings.HasPrefix(k, e.Name()+"/") && !b.finished())
		}
		if busy {
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.Dir, e.Name())); err == nil {
			for k := range m.wheels {
				if strings.HasPrefix(k, e.Name()+"/") {
					delete(m.wheels, k)
				}
			}
			m.log().Info("update mirror: release copy pruned", "event", "update_mirror_pruned", "version", e.Name())
		}
	}
}

// keepVersions are the releases whose copies stay after a run of version: the newest
// keep (keep_versions, at least 2) of the versions the mirror holds, version among them.
func keepVersions(held []string, version string, keep int) []string {
	if keep < 2 {
		keep = 2
	}
	all := append([]string{NormalizeVersion(version)}, held...)
	sort.SliceStable(all, func(i, j int) bool { return CompareVersions(all[i], all[j]) > 0 })
	var out []string
	seen := map[string]bool{}
	for _, v := range all {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		if len(out) < keep || v == NormalizeVersion(version) {
			out = append(out, v)
		}
	}
	return out
}

// Held is the versions the mirror holds copies of.
func (m *Mirror) Held() []string {
	if m == nil || m.Dir == "" {
		return nil
	}
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidVersion(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// platformSegment is a platform as a URL path segment: "linux/amd64" is "linux-amd64".
func platformSegment(platform string) string { return strings.ReplaceAll(platform, "/", "-") }

// platformOfSegment is the platform a URL segment names ("" for none).
func platformOfSegment(seg string) string {
	osName, arch, ok := strings.Cut(seg, "-")
	if !ok || osName == "" || arch == "" || strings.Contains(arch, "-") {
		return ""
	}
	p := osName + "/" + arch
	if _, ok := pipPlatforms[p]; !ok {
		return ""
	}
	return p
}

// retryAfter writes a Retry-After of d.
func retryAfter(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())))
}
