package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
)

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// mirrorRig is a home with one verified release (1.5.0) on a stand-in GitHub, and its
// mirror behind a router that marks a request with X-Test-Node as that machine's (the
// home's delegation middleware does it for a verified signature).
type mirrorRig struct {
	t      *testing.T
	store  *Store
	m      *Mirror
	router chi.Router
	files  map[string][]byte
	mu     sync.Mutex
	hits   map[string]int
	serve  map[string][]byte // what GitHub serves when it differs from files
}

func newMirrorRig(t *testing.T) *mirrorRig {
	t.Helper()
	rig := &mirrorRig{t: t, hits: map[string]int{}, serve: map[string][]byte{}}
	man := map[string]any{"v": 1, "version": "1.5.0", "tag": "v1.5.0", "channel": "stable",
		"assets": map[string]any{"linux/amd64": map[string]any{"archive": "maktaba-server_1.5.0_linux_amd64.tar.gz"}},
		"pipeline": map[string]any{"wheel": "maktaba_pipeline-1.5.0-py3-none-any.whl",
			"constraints": "maktaba_pipeline-1.5.0-constraints.txt", "python": "3.12"}}
	manJSON, _ := json.Marshal(man)
	rig.files = map[string][]byte{
		"release.json": manJSON,
		"maktaba-server_1.5.0_linux_amd64.tar.gz": []byte("the linux archive, 0123456789"),
		"maktaba_pipeline-1.5.0-py3-none-any.whl": []byte("the pipeline wheel"),
		"maktaba_pipeline-1.5.0-constraints.txt":  []byte("anyio==4.4.0 --hash=sha256:aa\ntorch==2.4.0 --hash=sha256:bb\n"),
	}
	var sums strings.Builder
	for _, n := range []string{"maktaba-server_1.5.0_linux_amd64.tar.gz", "maktaba_pipeline-1.5.0-constraints.txt",
		"maktaba_pipeline-1.5.0-py3-none-any.whl", "release.json"} {
		fmt.Fprintf(&sums, "%s  %s\n", hexSum(rig.files[n]), n)
	}
	rig.files[ChecksumsFile] = []byte(sums.String())
	rig.files[BundleFile] = []byte(`{"bundle":"the signature"}`)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		rig.mu.Lock()
		rig.hits[name]++
		b, ok := rig.serve[name]
		rig.mu.Unlock()
		if !ok {
			b, ok = rig.files[name]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(gh.Close)
	rig.store = &Store{DB: sqliteDB(t)}
	var assets []Asset
	for n := range rig.files {
		assets = append(assets, Asset{Name: n, URL: gh.URL + "/o/r/releases/download/v1.5.0/" + n})
	}
	ctx := context.Background()
	if _, err := rig.store.Seen(ctx, Release{Version: "1.5.0", Tag: "v1.5.0", Channel: ChannelStable, Assets: assets}); err != nil {
		t.Fatal(err)
	}
	if err := rig.store.SetVerified(ctx, "1.5.0", Verified{Manifest: Manifest{V: 1, Version: "1.5.0", Tag: "v1.5.0"},
		ChecksumsSHA256: hexSum(rig.files[ChecksumsFile])}); err != nil {
		t.Fatal(err)
	}
	rig.m = &Mirror{Store: rig.store, Dir: t.TempDir()}
	rig.router = chi.NewRouter()
	rig.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n := r.Header.Get("X-Test-Node"); n != "" {
				r = r.WithContext(delegation.WithNode(r.Context(), n))
			}
			next.ServeHTTP(w, r)
		})
	})
	rig.m.Mount(rig.router)
	return rig
}

func (rig *mirrorRig) get(path, node string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if node != "" {
		req.Header.Set("X-Test-Node", node)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	rig.router.ServeHTTP(rec, req)
	return rec
}

func (rig *mirrorRig) hit(name string) int {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	return rig.hits[name]
}

// A machine's signed request gets a verified file of the release, fetched from GitHub
// once; a range request gets its part; anyone else gets nothing.
func TestMirrorSignedRangeRequests(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	archive := "maktaba-server_1.5.0_linux_amd64.tar.gz"
	path := "/api/cluster/updates/artifacts/1.5.0/" + archive
	if rec := rig.get(path, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unsigned request: %d %s", rec.Code, rec.Body.String())
	}
	rec := rig.get(path, "node-1", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != string(rig.files[archive]) {
		t.Fatalf("the archive: %d %q", rec.Code, rec.Body.String())
	}
	if et := rec.Header().Get("ETag"); et != `"`+hexSum(rig.files[archive])+`"` {
		t.Fatalf("ETag %q", et)
	}
	rec = rig.get(path, "node-2", map[string]string{"Range": "bytes=4-8"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != string(rig.files[archive][4:9]) {
		t.Fatalf("a range: %d %q", rec.Code, rec.Body.String())
	}
	if n := rig.hit(archive); n != 1 {
		t.Fatalf("GitHub was asked %d times for the archive, want once", n)
	}
	for _, n := range []string{ChecksumsFile, BundleFile, "release.json"} {
		if rec := rig.get("/api/cluster/updates/artifacts/1.5.0/"+n, "node-1", nil); rec.Code != http.StatusOK ||
			rec.Body.String() != string(rig.files[n]) {
			t.Fatalf("%s: %d %q", n, rec.Code, rec.Body.String())
		}
	}
	// v-prefixed and plain versions are the same release
	if rec := rig.get("/api/cluster/updates/artifacts/v1.5.0/release.json", "node-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("v1.5.0: %d", rec.Code)
	}
}

// A name the release does not list, a release the home did not verify (or that was
// withdrawn) and a name with a path are 404s.
func TestMirrorUnknownName404(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	for _, p := range []string{
		"/api/cluster/updates/artifacts/1.5.0/latest.json", // published, not in checksums.txt
		"/api/cluster/updates/artifacts/1.5.0/nothing.tar.gz",
		"/api/cluster/updates/artifacts/1.6.0/release.json",
	} {
		if rec := rig.get(p, "node-1", nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	if err := rig.store.SetWithdrawn(context.Background(), "1.5.0", WithdrawnGone); err != nil {
		t.Fatal(err)
	}
	if rec := rig.get("/api/cluster/updates/artifacts/1.5.0/release.json", "node-1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a withdrawn release: %d", rec.Code)
	}
}

// A file GitHub serves with another digest than the signed checksums list is never kept
// (502: the machine goes to GitHub itself, and verifies there); once GitHub serves the
// right one it is.
func TestMirrorRefusesAFileThatDoesNotMatch(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	whl := "maktaba_pipeline-1.5.0-py3-none-any.whl"
	rig.serve[whl] = []byte("a wheel someone swapped")
	path := "/api/cluster/updates/artifacts/1.5.0/" + whl
	if rec := rig.get(path, "node-1", nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("a swapped wheel: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(rig.m.Dir, "1.5.0", whl)); err == nil {
		t.Fatal("the swapped wheel was kept")
	}
	delete(rig.serve, whl)
	if rec := rig.get(path, "node-1", nil); rec.Code != http.StatusOK || rec.Body.String() != string(rig.files[whl]) {
		t.Fatalf("the real wheel: %d %q", rec.Code, rec.Body.String())
	}
	// checksums.txt itself must be the one whose signature the catalog verified
	rig2 := newMirrorRig(t)
	rig2.serve[ChecksumsFile] = append([]byte(nil), append(rig2.files[ChecksumsFile], []byte("00  extra\n")...)...)
	if rec := rig2.get("/api/cluster/updates/artifacts/1.5.0/release.json", "node-1", nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("another checksums.txt: %d %s", rec.Code, rec.Body.String())
	}
}

// One machine has at most mirrorPerNode downloads open at once; the next is told to come
// back (429), and another machine is not held up.
func TestMirrorPerNodeLimit(t *testing.T) {
	t.Parallel()
	m := &Mirror{}
	req := func(node string) (*httptest.ResponseRecorder, func(), bool) {
		r := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(delegation.WithNode(context.Background(), node))
		rec := httptest.NewRecorder()
		done, ok := m.machine(rec, r)
		return rec, done, ok
	}
	var dones []func()
	for i := 0; i < mirrorPerNode; i++ {
		_, done, ok := req("node-1")
		if !ok {
			t.Fatalf("download %d refused", i)
		}
		dones = append(dones, done)
	}
	rec, _, ok := req("node-1")
	if ok || rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("one too many: ok=%v %d", ok, rec.Code)
	}
	if _, done, ok := req("node-2"); !ok {
		t.Fatal("another machine was held up")
	} else {
		done()
	}
	dones[0]()
	if _, done, ok := req("node-1"); !ok {
		t.Fatal("a slot freed was not taken")
	} else {
		done()
	}
}

// Prefetch fetches the signed files and each platform's files before any machine asks;
// Prune keeps the run's release and the newest keep_versions.
func TestMirrorPrefetchAndPrune(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	rig.m.Prefetch(context.Background(), "1.5.0", []string{"linux/amd64", "darwin/arm64"})
	want := []string{ChecksumsFile, BundleFile, "release.json", "maktaba-server_1.5.0_linux_amd64.tar.gz",
		"maktaba_pipeline-1.5.0-py3-none-any.whl", "maktaba_pipeline-1.5.0-constraints.txt"}
	deadline := time.Now().Add(10 * time.Second)
	for _, n := range want {
		for {
			if _, err := os.Stat(filepath.Join(rig.m.Dir, "1.5.0", n+okSuffix)); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s was not prefetched", n)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for _, v := range []string{"1.3.0", "1.4.0", "1.2.0"} {
		if err := os.MkdirAll(filepath.Join(rig.m.Dir, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rig.m.Prune(keepVersions(rig.m.Held(), "1.5.0", 3))
	if got := rig.m.Held(); strings.Join(got, ",") != "1.3.0,1.4.0,1.5.0" {
		t.Fatalf("held after the prune: %v, want the newest 3", got)
	}
}

func TestKeepVersions(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		held    []string
		version string
		keep    int
		want    string
	}{
		{[]string{"1.2.0", "1.3.0", "1.4.0"}, "1.5.0", 3, "1.5.0,1.4.0,1.3.0"},
		{[]string{"1.2.0", "1.6.0"}, "1.5.0", 1, "1.6.0,1.5.0"}, // at least 2, the run's always
		{[]string{"1.6.0", "1.7.0", "1.8.0"}, "1.5.0", 2, "1.8.0,1.7.0,1.5.0"},
	} {
		if got := strings.Join(keepVersions(c.held, c.version, c.keep), ","); got != c.want {
			t.Errorf("keepVersions(%v, %s, %d) = %s, want %s", c.held, c.version, c.keep, got, c.want)
		}
	}
}

// fakePip plays pip for the wheelhouse: the marker filter writes the pins it is given, and
// pip download writes a wheel per pin, but finds no wheel of torch for the platform.
func fakePip(t *testing.T) *[][]string {
	t.Helper()
	var mu sync.Mutex
	var calls [][]string
	old := pipRun
	pipRun = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, args)
		mu.Unlock()
		if len(args) > 2 && args[0] == "-I" {
			src, dst := args[3], args[4]
			b, err := os.ReadFile(src)
			if err != nil {
				return nil, err
			}
			return nil, os.WriteFile(dst, b, 0o644)
		}
		dest, pins := "", ""
		for i, a := range args {
			switch a {
			case "--dest":
				dest = args[i+1]
			case "-r":
				pins = args[i+1]
			}
		}
		b, _ := os.ReadFile(pins)
		if strings.Contains(string(b), "torch==") {
			return []byte("ERROR: No matching distribution found for torch==2.4.0"), errors.New("exit status 1")
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			name, ver, _ := strings.Cut(strings.Fields(line)[0], "==")
			if err := os.WriteFile(filepath.Join(dest, name+"-"+ver+"-py3-none-any.whl"), []byte("wheel "+name), 0o644); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	t.Cleanup(func() { pipRun = old })
	return &calls
}

// A machine's first ask starts the platform's wheelhouse (202 while pip downloads it);
// then it gets the index, each wheel by name, and the pins with no wheel for the platform
// listed (it takes those from PyPI).
func TestWheelhouseBuildsAndServes(t *testing.T) {
	rig := newMirrorRig(t)
	calls := fakePip(t)
	rig.m.Python = func() string { return "python3" }
	path := "/api/cluster/updates/wheelhouse/1.5.0/linux-amd64"
	var idx WheelIndex
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := rig.get(path, "node-1", nil)
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
				t.Fatal(err)
			}
			break
		}
		if rec.Code != http.StatusAccepted || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("while it is downloaded: %d %s", rec.Code, rec.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatal("the wheelhouse was never ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(idx.Files) != 1 || idx.Files[0].Name != "anyio-4.4.0-py3-none-any.whl" || idx.Python != "3.12" ||
		strings.Join(idx.Missing, ",") != "torch==2.4.0" || idx.Platform != "linux/amd64" {
		t.Fatalf("index = %+v", idx)
	}
	rec := rig.get(path+"/anyio-4.4.0-py3-none-any.whl", "node-1", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "wheel anyio" ||
		rec.Header().Get("ETag") != `"`+hexSum([]byte("wheel anyio"))+`"` {
		t.Fatalf("the wheel: %d %q", rec.Code, rec.Body.String())
	}
	if rec := rig.get(path+"/torch-2.4.0-py3-none-any.whl", "node-1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a wheel it does not hold: %d", rec.Code)
	}
	// pip was asked for the platform's wheels for the release's Python, by hash
	var download []string
	for _, c := range *calls {
		if len(c) > 2 && c[1] == "pip" {
			download = c
		}
	}
	got := strings.Join(download, " ")
	for _, w := range []string{"--only-binary=:all:", "--require-hashes", "--python-version 3.12", "--abi cp312",
		"--platform manylinux_2_17_x86_64", "--no-deps"} {
		if !strings.Contains(got, w) {
			t.Errorf("pip download lacks %q: %s", w, got)
		}
	}
	if rec := rig.get("/api/cluster/updates/wheelhouse/1.5.0/plan9-mips", "node-1", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("a platform it does not know: %d", rec.Code)
	}
	if rec := rig.get(path, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", rec.Code)
	}
}

// With the rules' wheelhouse off, or no Python for pip, the home keeps none: a 404, and
// the machine builds from PyPI.
func TestWheelhouseOff(t *testing.T) {
	rig := newMirrorRig(t)
	fakePip(t)
	path := "/api/cluster/updates/wheelhouse/1.5.0/linux-amd64"
	if rec := rig.get(path, "node-1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no Python: %d", rec.Code)
	}
	rig.m.Python = func() string { return "python3" }
	rig.m.Enabled = func(context.Context) bool { return false }
	if rec := rig.get(path, "node-1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("the rule off: %d", rec.Code)
	}
}

// The marker filter keeps the pins whose markers hold on the platform, with their hashes,
// across uv export's continuation lines and comments (a real Python, when there is one).
func TestPinsForPlatform(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("runs Python; runs without -short (the integration tier)")
	}
	py := ""
	for _, n := range []string{"python3", "python"} {
		if p, err := exec.LookPath(n); err == nil {
			py = p
			break
		}
	}
	if py == "" {
		t.Skip("no Python on PATH")
	}
	if out, err := exec.Command(py, "-c", "import pip._vendor.packaging").CombinedOutput(); err != nil {
		t.Skipf("this Python has no pip: %s", out)
	}
	dir := t.TempDir()
	cons := filepath.Join(dir, "constraints.txt")
	if err := os.WriteFile(cons, []byte(`# This file was autogenerated by uv
anyio==4.4.0 \
    --hash=sha256:aa \
    --hash=sha256:ab
    # via httpx
mlx==0.18.0 ; sys_platform == 'darwin' and platform_machine == 'arm64' \
    --hash=sha256:cc
pywin32==306 ; sys_platform == 'win32' \
    --hash=sha256:dd
nvidia-cublas-cu12==12.1.3.1 ; platform_machine == 'x86_64' and sys_platform == 'linux' \
    --hash=sha256:ee
`), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(platform string) string {
		out := filepath.Join(dir, "pins-"+platformSegment(platform))
		env, _ := json.Marshal(markerEnv(pipPlatforms[platform], "3.12"))
		if b, err := exec.Command(py, "-I", "-c", pinsForPlatform, cons, out, string(env)).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", platform, err, b)
		}
		b, _ := os.ReadFile(out)
		return strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	}
	if got := run("linux/amd64"); got != "anyio==4.4.0 --hash=sha256:aa --hash=sha256:ab\nnvidia-cublas-cu12==12.1.3.1 --hash=sha256:ee" {
		t.Errorf("linux/amd64:\n%s", got)
	}
	if got := run("darwin/arm64"); got != "anyio==4.4.0 --hash=sha256:aa --hash=sha256:ab\nmlx==0.18.0 --hash=sha256:cc" {
		t.Errorf("darwin/arm64:\n%s", got)
	}
	if got := run("windows/amd64"); got != "anyio==4.4.0 --hash=sha256:aa --hash=sha256:ab\npywin32==306 --hash=sha256:dd" {
		t.Errorf("windows/amd64:\n%s", got)
	}
}

// dropPin takes one pin out by its normalized name.
func TestDropPin(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "pins")
	if err := os.WriteFile(p, []byte("anyio==4.4.0 --hash=sha256:aa\nTorch_Audio==2.4.0 --hash=sha256:bb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pin, ok, err := dropPin(p, "torch-audio")
	if err != nil || !ok || pin != "Torch_Audio==2.4.0" {
		t.Fatalf("dropPin = %q %v %v", pin, ok, err)
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != "anyio==4.4.0 --hash=sha256:aa" {
		t.Fatalf("left %q", b)
	}
	if _, ok, _ := dropPin(p, "nothing"); ok {
		t.Fatal("dropped a pin that is not there")
	}
}

// The home passes Sigstore's TUF repository on to its machines (design §3.3): the files
// that never change are kept, the timestamp for a few minutes (and served from the last
// copy when the public repository is out of reach), the public repository's 404 passed on
// (a TUF client looks for the next root until one), and nothing but the repository's names.
func TestMirrorPassesOnTheTUFRepository(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	target := "targets/" + hexSum([]byte(`{"trusted":"root"}`)) + ".trusted_root.json"
	files := map[string]string{"1.root.json": `{"root":1}`, "timestamp.json": `{"ts":1}`, target: `{"trusted":"root"}`}
	var mu sync.Mutex
	hits := map[string]int{}
	down := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		mu.Lock()
		hits[name]++
		isDown := down
		mu.Unlock()
		if isDown {
			panic(http.ErrAbortHandler)
		}
		b, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, b)
	}))
	t.Cleanup(up.Close)
	now := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	rig.m.TUFUpstream = up.URL
	rig.m.Now = func() time.Time { return now }
	get := func(name string) *httptest.ResponseRecorder {
		return rig.get("/api/cluster/updates/tuf/"+name, "node-1", nil)
	}
	for _, n := range []string{"1.root.json", target, "timestamp.json"} {
		for i := 0; i < 2; i++ {
			if rec := get(n); rec.Code != http.StatusOK || rec.Body.String() != files[n] {
				t.Fatalf("%s: %d %q", n, rec.Code, rec.Body.String())
			}
		}
		mu.Lock()
		h := hits[n]
		mu.Unlock()
		if h != 1 {
			t.Fatalf("%s read %d times from the public repository, want once", n, h)
		}
	}
	if rec := get("2.root.json"); rec.Code != http.StatusNotFound {
		t.Fatalf("the next root: %d", rec.Code)
	}
	for _, n := range []string{"keys.json", "targets/trusted_root.json", "1.root.json.bak"} {
		if rec := get(n); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", n, rec.Code)
		}
	}
	// the timestamp is read again after its few minutes; the repository away, the last one
	now = now.Add(tufTimestampTTL + time.Second)
	files["timestamp.json"] = `{"ts":2}`
	if rec := get("timestamp.json"); rec.Body.String() != `{"ts":2}` {
		t.Fatalf("a fresh timestamp: %q", rec.Body.String())
	}
	now = now.Add(tufTimestampTTL + time.Second)
	mu.Lock()
	down = true
	mu.Unlock()
	if rec := get("timestamp.json"); rec.Code != http.StatusOK || rec.Body.String() != `{"ts":2}` {
		t.Fatalf("the repository away: %d %q", rec.Code, rec.Body.String())
	}
	if rec := rig.get("/api/cluster/updates/tuf/1.root.json", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", rec.Code)
	}
	// the prune leaves the repository's files
	rig.m.Prune(nil)
	if _, err := os.Stat(filepath.Join(rig.m.Dir, "tuf", "1.root.json")); err != nil {
		t.Fatal("the prune took the TUF repository's files")
	}
}

// The mirror keeps nothing that would leave the home's disk without room: the machine goes
// to GitHub (a 502), and no wheelhouse is downloaded (its machines use PyPI).
func TestMirrorLeavesRoomOnTheDisk(t *testing.T) {
	rig := newMirrorRig(t)
	fakePip(t)
	rig.m.Python = func() string { return "python3" }
	rig.m.FreeDisk = func(string) (uint64, error) { return 1 << 30, nil }
	rec := rig.get("/api/cluster/updates/artifacts/1.5.0/release.json", "node-1", nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "GiB free") {
		t.Fatalf("a full disk: %d %s", rec.Code, rec.Body.String())
	}
	rig.m.FreeDisk = func(string) (uint64, error) { return 3 << 30, nil } // room for the files, not a wheelhouse
	path := "/api/cluster/updates/wheelhouse/1.5.0/linux-amd64"
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := rig.get(path, "node-1", nil)
		if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "GiB free") {
			break
		}
		if rec.Code != http.StatusAccepted || time.Now().After(deadline) {
			t.Fatalf("a wheelhouse without room: %d %s", rec.Code, rec.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The driver asks the mirror once per run for the run's release, for the platforms the
// cluster's machines run (their rows and their agents' word).
func TestDriverPrefetchesTheRunsRelease(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	if _, err := rig.store.DB.Exec(`INSERT INTO cluster_nodes (id, name, state, platform, full_node) VALUES
		('n1', 'studio', 'approved', 'linux-amd64', 1), ('n2', 'old', 'revoked', 'windows/amd64', 1)`); err != nil {
		t.Fatal(err)
	}
	d := &Driver{Store: rig.store, Mirror: rig.m}
	if got := strings.Join(d.platforms(context.Background()), ","); got != "linux/amd64" {
		t.Fatalf("platforms = %s", got)
	}
	run := Run{ID: "run-1", Version: "1.5.0"}
	d.prefetch(context.Background(), run)
	archive := filepath.Join(rig.m.Dir, "1.5.0", "maktaba-server_1.5.0_linux_amd64.tar.gz"+okSuffix)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(archive); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run's release was not prefetched")
		}
		time.Sleep(20 * time.Millisecond)
	}
	d.prefetch(context.Background(), run)
	time.Sleep(50 * time.Millisecond)
	if n := rig.hit("maktaba-server_1.5.0_linux_amd64.tar.gz"); n != 1 {
		t.Fatalf("the archive fetched %d times", n)
	}
}

// A version, platform or file name that would reach a path outside the mirror's folder is
// refused before any file is touched (400): "..", encoded dots and slashes, backslashes,
// absolute paths and drives, on the release files, the wheelhouse index and its wheels.
func TestMirrorRefusesPathsOutsideItsFolder(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	for _, p := range []string{
		"/api/cluster/updates/artifacts/1.5.0/..%2Fx",
		"/api/cluster/updates/artifacts/1.5.0/..",
		"/api/cluster/updates/artifacts/1.5.0/%2e%2e",
		"/api/cluster/updates/artifacts/1.5.0/%2e%2e%2fsecrets",
		"/api/cluster/updates/artifacts/1.5.0/..%5C..%5Cwin.ini",
		"/api/cluster/updates/artifacts/1.5.0/a..b",
		"/api/cluster/updates/artifacts/1.5.0/%2Fetc%2Fpasswd",
		"/api/cluster/updates/artifacts/1.5.0/C:%5CWindows%5Cwin.ini",
		"/api/cluster/updates/artifacts/..%2F..%2Fetc/passwd",
		"/api/cluster/updates/artifacts/not-a-version/release.json",
		"/api/cluster/updates/wheelhouse/..%2F1.5.0/linux-amd64",
		"/api/cluster/updates/wheelhouse/1.5.0/..%2Flinux-amd64",
		"/api/cluster/updates/wheelhouse/1.5.0/linux-amd64/..%2F..%2Fx.whl",
		"/api/cluster/updates/wheelhouse/1.5.0/linux-amd64/%2e%2e",
	} {
		if rec := rig.get(p, "node-1", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

// inside keeps every path a request names inside its root.
func TestInsideStaysInItsRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if p, err := inside(root, "1.5.0", "release.json"); err != nil || filepath.Dir(filepath.Dir(p)) != root {
		t.Fatalf("a plain name: %q %v", p, err)
	}
	for _, parts := range [][]string{
		{".."}, {"1.5.0", ".."}, {"../x"}, {`..\x`}, {"a/b"}, {`a\b`}, {"C:x"}, {"/etc"}, {""}, {"."},
		{"1.5.0", "..%2f"},
	} {
		if p, err := inside(root, parts...); !errors.Is(err, errOutside) {
			t.Errorf("%q: %q %v", parts, p, err)
		}
	}
}

// A kept copy that no longer verifies (a power cut before its data reached the disk, a
// damaged block, an edit) is dropped and fetched again: by the next process, which checks
// each kept file once, and by this one when the file changed under it. Story 35.12 review F1.
func TestMirrorDamagedKeptCopyIsFetchedAgain(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	archive := "maktaba-server_1.5.0_linux_amd64.tar.gz"
	path := "/api/cluster/updates/artifacts/1.5.0/" + archive
	if rec := rig.get(path, "node-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	kept := filepath.Join(rig.m.Dir, "1.5.0", archive)
	if err := os.Truncate(kept, 0); err != nil { // the data never reached the disk
		t.Fatal(err)
	}
	// the next process (the API restarted after the power cut)
	rig.m = &Mirror{Store: rig.store, Dir: rig.m.Dir}
	rig.router = chi.NewRouter()
	rig.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(delegation.WithNode(r.Context(), r.Header.Get("X-Test-Node"))))
		})
	})
	rig.m.Mount(rig.router)
	rec := rig.get(path, "node-1", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != string(rig.files[archive]) {
		t.Fatalf("after the damage: %d %q", rec.Code, rec.Body.String())
	}
	if n := rig.hit(archive); n != 2 {
		t.Fatalf("GitHub asked %d times, want twice (the damaged copy fetched again)", n)
	}
	// changed under the running process (another length, another time): checked again
	if err := os.WriteFile(kept, []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := rig.get(path, "node-2", nil); rec.Body.String() != string(rig.files[archive]) || rig.hit(archive) != 3 {
		t.Fatalf("an edited copy: %q, GitHub asked %d times", rec.Body.String(), rig.hit(archive))
	}
	// an intact copy is hashed once, then served as it is
	if rec := rig.get(path, "node-3", nil); rec.Code != http.StatusOK || rig.hit(archive) != 3 {
		t.Fatalf("an intact copy: %d, GitHub asked %d times", rec.Code, rig.hit(archive))
	}
}

// The home takes a file from its own staged release (bin/releases/<v>: the signed files,
// the pipeline's wheel and constraints) when it verifies, rather than downloading it again;
// a copy that does not verify is passed over. Design §13.2.
func TestMirrorReusesTheHomesOwnCopies(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	rel := filepath.Join(t.TempDir(), "bin", "releases", "1.5.0")
	pipe := filepath.Join(rel, "pipeline")
	if err := os.MkdirAll(pipe, 0o755); err != nil {
		t.Fatal(err)
	}
	whl, cons := "maktaba_pipeline-1.5.0-py3-none-any.whl", "maktaba_pipeline-1.5.0-constraints.txt"
	for name, b := range map[string][]byte{filepath.Join(rel, "release.json"): rig.files["release.json"],
		filepath.Join(pipe, whl): rig.files[whl], filepath.Join(pipe, cons): []byte("not the constraints")} {
		if err := os.WriteFile(name, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rig.m.Local = func(version string) []string {
		if version != "1.5.0" {
			return nil
		}
		return []string{rel, pipe}
	}
	for _, n := range []string{"release.json", whl, cons} {
		if rec := rig.get("/api/cluster/updates/artifacts/1.5.0/"+n, "node-1", nil); rec.Code != http.StatusOK ||
			rec.Body.String() != string(rig.files[n]) {
			t.Fatalf("%s: %d %q", n, rec.Code, rec.Body.String())
		}
	}
	if rig.hit("release.json") != 0 || rig.hit(whl) != 0 {
		t.Fatalf("GitHub asked for a file the home holds: release.json %d, wheel %d", rig.hit("release.json"), rig.hit(whl))
	}
	if rig.hit(cons) != 1 {
		t.Fatalf("a home copy that does not verify was used (GitHub asked %d times)", rig.hit(cons))
	}
}

// A kept TUF file that is not whole (a target whose digest is not its name's, metadata that
// is not JSON) is read again from the public repository rather than passed on.
func TestMirrorTUFCacheIsCheckedBeforeUse(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	body := `{"trusted":"root"}`
	target := "targets/" + hexSum([]byte(body)) + ".trusted_root.json"
	hits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(up.Close)
	rig.m.TUFUpstream = up.URL
	if rec := rig.get("/api/cluster/updates/tuf/"+target, "node-1", nil); rec.Body.String() != body {
		t.Fatalf("first: %q", rec.Body.String())
	}
	kept := filepath.Join(rig.m.Dir, "tuf", filepath.FromSlash(target))
	if err := os.WriteFile(kept, []byte(`{"trusted":"roo`), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := rig.get("/api/cluster/updates/tuf/"+target, "node-1", nil); rec.Body.String() != body || hits != 2 {
		t.Fatalf("a damaged copy: %q (read %d times)", rec.Body.String(), hits)
	}
	if !tufWhole("3.root.json", []byte(`{"a":1}`)) || tufWhole("3.root.json", []byte(`{"a":`)) {
		t.Fatal("tufWhole on metadata")
	}
}
