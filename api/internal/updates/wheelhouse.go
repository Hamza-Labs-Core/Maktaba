package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// WheelIndex is a wheelhouse's index.json: its wheels (each pinned by its hash in the
// release's constraints too) and the pins it could not download for the platform (a
// package with no binary wheel there: a machine installs those from PyPI).
type WheelIndex struct {
	Version  string      `json:"version"`
	Platform string      `json:"platform"`
	Python   string      `json:"python"`
	Files    []WheelFile `json:"files"`
	Missing  []string    `json:"missing,omitempty"`
	BuiltAt  time.Time   `json:"built_at"`
}

// WheelFile is one wheel of a wheelhouse.
type WheelFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// wheelIndexFile is written last into a finished wheelhouse.
const wheelIndexFile = "index.json"

// wheelRetry is how long a wheelhouse that could not be downloaded waits before it is
// tried again (a machine meanwhile installs from PyPI).
const wheelRetry = 10 * time.Minute

// wheelhouseRoom is the room a wheelhouse needs on the home's disk.
const wheelhouseRoom uint64 = 6 << 30

// wheelBuild is one release's wheelhouse for one platform being downloaded (or failed).
type wheelBuild struct {
	done chan struct{}
	err  error
	at   time.Time
}

func (b *wheelBuild) finished() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

// pipTarget is a platform as pip downloads for it: its wheel platform tags (the most
// widely compatible first, so pip prefers those) and its environment markers' values.
type pipTarget struct {
	tags []string
	env  map[string]string
}

func manylinux(arch string) []string {
	var tags []string
	for _, glibc := range []string{"2_17", "2_24", "2_26", "2_27", "2_28", "2_31", "2_34", "2_35"} {
		tags = append(tags, "manylinux_"+glibc+"_"+arch)
	}
	return append(tags, "manylinux2014_"+arch)
}

func macos(arch string, from int) []string {
	var tags []string
	if from <= 10 {
		for _, minor := range []int{9, 10, 11, 12, 13, 14, 15} {
			tags = append(tags, fmt.Sprintf("macosx_10_%d_%s", minor, arch), fmt.Sprintf("macosx_10_%d_universal2", minor))
		}
		from = 11
	}
	for major := from; major <= 15; major++ {
		tags = append(tags, fmt.Sprintf("macosx_%d_0_%s", major, arch), fmt.Sprintf("macosx_%d_0_universal2", major))
	}
	return tags
}

// pipPlatforms are the platforms a wheelhouse is downloaded for (release.json's os/arch).
var pipPlatforms = map[string]pipTarget{
	"linux/amd64": {tags: manylinux("x86_64"), env: map[string]string{"sys_platform": "linux",
		"platform_system": "Linux", "platform_machine": "x86_64", "os_name": "posix"}},
	"linux/arm64": {tags: manylinux("aarch64"), env: map[string]string{"sys_platform": "linux",
		"platform_system": "Linux", "platform_machine": "aarch64", "os_name": "posix"}},
	"darwin/arm64": {tags: macos("arm64", 11), env: map[string]string{"sys_platform": "darwin",
		"platform_system": "Darwin", "platform_machine": "arm64", "os_name": "posix"}},
	"darwin/amd64": {tags: macos("x86_64", 10), env: map[string]string{"sys_platform": "darwin",
		"platform_system": "Darwin", "platform_machine": "x86_64", "os_name": "posix"}},
	"windows/amd64": {tags: []string{"win_amd64"}, env: map[string]string{"sys_platform": "win32",
		"platform_system": "Windows", "platform_machine": "AMD64", "os_name": "nt"}},
	"windows/arm64": {tags: []string{"win_arm64"}, env: map[string]string{"sys_platform": "win32",
		"platform_system": "Windows", "platform_machine": "ARM64", "os_name": "nt"}},
}

// pythonOf is the release's pipeline Python as "3.12" (its default when it does not say).
func pythonOf(man *releaseverify.Manifest) string {
	if man.Pipeline != nil {
		parts := strings.SplitN(strings.TrimSpace(man.Pipeline.Python), ".", 3)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0] + "." + parts[1]
		}
	}
	return "3.12"
}

// wheelDir is a release's wheelhouse for platform.
func (m *Mirror) wheelDir(version, platform string) string {
	return filepath.Join(m.Dir, version, "wheelhouse", platformSegment(platform))
}

// readWheelIndex is a finished wheelhouse's index.
func (m *Mirror) readWheelIndex(version, platform string) (*WheelIndex, error) {
	b, err := os.ReadFile(filepath.Join(m.wheelDir(version, platform), wheelIndexFile))
	if err != nil {
		return nil, err
	}
	var idx WheelIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func (m *Mirror) wheelhouseOn(ctx context.Context) bool {
	if m.Enabled != nil && !m.Enabled(ctx) {
		return false
	}
	return m.Python != nil && strings.TrimSpace(m.Python()) != ""
}

// startWheelhouse starts the download of a release's wheelhouse for platform unless it is
// there, under way, or failed less than wheelRetry ago; the build in hand.
func (m *Mirror) startWheelhouse(ctx context.Context, rel Release, man *releaseverify.Manifest, platform string) *wheelBuild {
	if man.Pipeline == nil || man.Pipeline.Constraints == "" || !m.wheelhouseOn(ctx) {
		return nil
	}
	if _, ok := pipPlatforms[platform]; !ok {
		return nil
	}
	if _, err := m.readWheelIndex(rel.Version, platform); err == nil {
		b := &wheelBuild{done: make(chan struct{})}
		close(b.done)
		return b
	}
	key := rel.Version + "/" + platform
	m.mu.Lock()
	if m.wheels == nil {
		m.wheels = map[string]*wheelBuild{}
	}
	if b, ok := m.wheels[key]; ok && (!b.finished() || b.err == nil || m.now().Sub(b.at) < wheelRetry) {
		m.mu.Unlock()
		return b
	}
	b := &wheelBuild{done: make(chan struct{}), at: m.now()}
	m.wheels[key] = b
	m.mu.Unlock()
	go func() {
		// one wheelhouse at a time: each is a large download
		wheelSlot <- struct{}{}
		defer func() { <-wheelSlot }()
		b.err = m.buildWheelhouse(context.WithoutCancel(ctx), rel, man, platform)
		m.mu.Lock()
		b.at = m.now()
		m.mu.Unlock()
		if b.err != nil {
			m.log().Warn("update mirror: the wheelhouse could not be downloaded (its machines install from PyPI)",
				"event", "update_wheelhouse_failed", "version", rel.Version, "platform", platform, "err", b.err.Error())
		}
		close(b.done)
	}()
	return b
}

// wheelSlot lets one wheelhouse download run at a time.
var wheelSlot = make(chan struct{}, 1)

// pipRun runs pip (`python args...`) with env; its combined output. Tests replace it.
var pipRun = func(ctx context.Context, python string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// pipMissing finds the pin pip found no wheel of ("No matching distribution found for
// torch==2.4.0").
var pipMissing = regexp.MustCompile(`(?i)(?:no matching distribution found for|could not find a version that satisfies the requirement)\s+([A-Za-z0-9][A-Za-z0-9._-]*)`)

// buildWheelhouse downloads a release's pinned pipeline dependencies for platform with pip
// (`pip download --only-binary=:all: --require-hashes --platform … --python-version …`)
// into its wheelhouse, after keeping only the pins whose markers hold there. A pin with no
// binary wheel for the platform is left out and listed (its machines take it from PyPI).
func (m *Mirror) buildWheelhouse(ctx context.Context, rel Release, man *releaseverify.Manifest, platform string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	python := strings.TrimSpace(m.Python())
	// a platform's pipeline packages are a few GB (torch)
	if err := m.room(wheelhouseRoom); err != nil {
		return err
	}
	cons, _, err := m.Artifact(ctx, rel.Version, man.Pipeline.Constraints)
	if err != nil {
		return err
	}
	parent := filepath.Dir(m.wheelDir(rel.Version, platform))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "."+platformSegment(platform)+".")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	target := pipPlatforms[platform]
	py := pythonOf(man)
	envJSON, _ := json.Marshal(markerEnv(target, py))
	pins := filepath.Join(tmp, ".pins.txt")
	env := m.pipEnv()
	if out, err := pipRun(ctx, python, []string{"-I", "-c", pinsForPlatform, cons, pins, string(envJSON)}, env); err != nil {
		return fmt.Errorf("the constraints could not be read for %s: %v: %s", platform, err, tail(out, 400))
	}
	var missing []string
	for try := 0; ; try++ {
		args := []string{"-m", "pip", "download", "--disable-pip-version-check", "--no-input", "--no-deps",
			"--only-binary=:all:", "--require-hashes", "--dest", tmp, "--python-version", py,
			"--implementation", "cp", "--abi", "cp" + strings.ReplaceAll(py, ".", ""), "--abi", "abi3", "--abi", "none"}
		for _, t := range target.tags {
			args = append(args, "--platform", t)
		}
		args = append(args, "-r", pins)
		out, err := pipRun(ctx, python, args, env)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := ""
		if mm := pipMissing.FindSubmatch(out); mm != nil {
			name = string(mm[1])
		}
		if name == "" || try >= 40 {
			return fmt.Errorf("pip download for %s: %v: %s", platform, err, tail(out, 600))
		}
		pin, ok, err := dropPin(pins, name)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("pip download for %s: %v: %s", platform, err, tail(out, 600))
		}
		missing = append(missing, pin)
	}
	idx := WheelIndex{Version: rel.Version, Platform: platform, Python: py, Missing: missing, BuiltAt: m.now().UTC()}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".whl") {
			continue
		}
		sum, size, err := fileSHA256(filepath.Join(tmp, e.Name()))
		if err != nil {
			return err
		}
		idx.Files = append(idx.Files, WheelFile{Name: e.Name(), SHA256: sum, Size: size})
	}
	sort.Slice(idx.Files, func(i, j int) bool { return idx.Files[i].Name < idx.Files[j].Name })
	_ = os.Remove(pins)
	b, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, wheelIndexFile), b, 0o644); err != nil {
		return err
	}
	final := m.wheelDir(rel.Version, platform)
	_ = os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	m.log().Info("update mirror: wheelhouse ready", "event", "update_wheelhouse_ready", "version", rel.Version,
		"platform", platform, "wheels", len(idx.Files), "missing", len(missing))
	return nil
}

// markerEnv is the environment markers' values on a machine of target with Python py.
func markerEnv(target pipTarget, py string) map[string]string {
	env := map[string]string{"python_version": py, "python_full_version": py + ".0",
		"implementation_name": "cpython", "platform_python_implementation": "CPython", "implementation_version": py + ".0"}
	for k, v := range target.env {
		env[k] = v
	}
	return env
}

// pipEnv is pip's environment: the API's (its proxy, index and certificate settings), with
// nothing that changes the Python that runs it.
func (m *Mirror) pipEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "PYTHONPATH", "PYTHONHOME", "VIRTUAL_ENV", "PIP_REQUIRE_VIRTUALENV", "PIP_NO_INDEX", "PIP_FIND_LINKS":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "PIP_DISABLE_PIP_VERSION_CHECK=1", "PYTHONUTF8=1", "PYTHONNOUSERSITE=1")
}

// dropPin removes name's pin from a pins file; the pin removed.
func dropPin(path, name string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	norm := func(s string) string { return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(s)) }
	var kept []string
	dropped := ""
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		pin := strings.Fields(line)
		if len(pin) > 0 && dropped == "" {
			n := pin[0]
			if i := strings.IndexAny(n, "=<>!~"); i >= 0 {
				n = n[:i]
			}
			if norm(n) == norm(name) {
				dropped = pin[0]
				continue
			}
		}
		kept = append(kept, line)
	}
	if dropped == "" {
		return "", false, nil
	}
	return dropped, true, os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

// fileSHA256 is a file's SHA-256 and size.
func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// pinsForPlatform writes the pins of a constraints file whose markers hold in the given
// environment, each with its hashes: argv = constraints, output, environment (JSON). It
// runs with the pipeline's Python, whose pip carries packaging.
const pinsForPlatform = `
import json, sys
try:
    from packaging.requirements import Requirement
except ImportError:
    from pip._vendor.packaging.requirements import Requirement
src, dst, env = sys.argv[1], sys.argv[2], json.loads(sys.argv[3])
lines, cur = [], ""
with open(src, encoding="utf-8") as f:
    for raw in f:
        line = raw.rstrip("\r\n")
        if line.lstrip().startswith("#"):
            line = ""
        line = line.split(" #", 1)[0].rstrip()
        if line.endswith("\\"):
            cur += line[:-1] + " "
            continue
        cur += line
        if cur.strip():
            lines.append(cur.strip())
        cur = ""
if cur.strip():
    lines.append(cur.strip())
out = []
for line in lines:
    if line.startswith("-"):
        continue
    toks = line.split()
    hashes = [t for t in toks if t.startswith("--hash=")]
    spec = " ".join(t for t in toks if not t.startswith("--hash="))
    try:
        req = Requirement(spec)
    except Exception:
        continue
    if req.marker is not None:
        try:
            if not req.marker.evaluate(env):
                continue
        except Exception:
            pass
    out.append(" ".join([req.name + str(req.specifier)] + hashes))
with open(dst, "w", encoding="utf-8") as f:
    f.write("\n".join(out) + "\n")
print(len(out))
`

func (m *Mirror) serveWheelIndex(w http.ResponseWriter, r *http.Request) {
	done, ok := m.machine(w, r)
	if !ok {
		return
	}
	defer done()
	platform := platformOfSegment(chi.URLParam(r, "platform"))
	if platform == "" {
		httperror.Write(w, r, httperror.NotFound("no wheelhouse for that platform"))
		return
	}
	rel, err := m.release(r.Context(), chi.URLParam(r, "version"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if idx, err := m.readWheelIndex(rel.Version, platform); err == nil {
		common.WriteJSON(w, r, http.StatusOK, idx)
		return
	}
	man, err := m.manifest(r.Context(), rel)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	b := m.startWheelhouse(r.Context(), rel, man, platform)
	switch {
	case b == nil:
		httperror.Write(w, r, httperror.NotFound("the home keeps no wheelhouse for "+platform))
	case b.finished() && b.err != nil:
		httperror.Write(w, r, httperror.NotFound("the wheelhouse for "+platform+" could not be downloaded: "+b.err.Error()))
	case b.finished():
		if idx, err := m.readWheelIndex(rel.Version, platform); err == nil {
			common.WriteJSON(w, r, http.StatusOK, idx)
			return
		}
		httperror.Write(w, r, httperror.NotFound("no wheelhouse for "+platform))
	default:
		retryAfter(w, 30*time.Second)
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"state": "building", "version": rel.Version,
			"platform": platform})
	}
}

func (m *Mirror) serveWheel(w http.ResponseWriter, r *http.Request) {
	done, ok := m.machine(w, r)
	if !ok {
		return
	}
	defer done()
	platform := platformOfSegment(chi.URLParam(r, "platform"))
	file := chi.URLParam(r, "file")
	if platform == "" || !mirrorName.MatchString(file) {
		httperror.Write(w, r, httperror.NotFound("no such wheel"))
		return
	}
	rel, err := m.release(r.Context(), chi.URLParam(r, "version"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	idx, err := m.readWheelIndex(rel.Version, platform)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("no wheelhouse for "+platform))
		return
	}
	for _, f := range idx.Files {
		if f.Name == file {
			serveFile(w, r, filepath.Join(m.wheelDir(rel.Version, platform), file), file, f.SHA256)
			return
		}
	}
	httperror.Write(w, r, httperror.NotFound("the wheelhouse holds no "+file))
}
