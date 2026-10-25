package main

// `maktaba-server setup --node` (Epic 35 story 35.8 AC7, design §9.4): turns a
// worker-only node — the Epic 31 pipeline worker its installer registered — into a full
// node, the same maktaba-server the home runs, in node mode:
//
//  1. keeps node.json (identity, key, credentials, home) untouched; refuses a config
//     joined before the channel's handshake v2 (no home.cluster_pub: re-join, EC2);
//  2. takes the home's current release — from --from (a directory holding the release
//     assets, or an extracted release), else the home's mirror, else GitHub Releases —
//     and verifies it in process (the Sigstore bundle of checksums.txt with the pinned
//     identity, every file against checksums.txt and release.json) before anything is
//     installed;
//  3. installs it side by side with the same code as `setup --self-managed`
//     (selfmanaged.Convert: bin/releases/<v>/, .verified, current / previous /
//     watermark, maktaba-launcher at the root — design §2.1, story 35.2);
//  4. builds the pipeline's environment from the release's wheel with --require-hashes
//     (or keeps --python, the worker's existing interpreter);
//  5. writes [pipeline] instances = N into the node's server.toml (--instances);
//  6. registers the launcher's service like `setup --service` (Windows: a logon task
//     by default, mapped drives are per user, or --mode service; Linux: a systemd
//     user unit; macOS: a LaunchAgent) and starts it;
//  7. waits until the new service answers as a node, then removes the old worker
//     units (maktaba-node, maktaba-node@N), never before.
//
// --converting is the rollout's update.convert (the release was downloaded and verified
// by the pipeline's agent; --from is then this executable's directory). --dry-run
// prints the plan and changes nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfmanaged"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfupdate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// nodeSetupFlags are setup's --node options. Flags another setup mode already defines
// (--root, --from, --version, --dry-run of --self-managed) are shared, not redefined.
type nodeSetupFlags struct {
	node bool
}

func (f *nodeSetupFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.node, "node", false, "convert this worker-only node into a full node (maktaba-server in node mode)")
	def := func(name, value, usage string) {
		if fl.Lookup(name) == nil {
			fl.String(name, value, usage)
		}
	}
	defBool := func(name, usage string) {
		if fl.Lookup(name) == nil {
			fl.Bool(name, false, usage)
		}
	}
	// --root, --from, --version and --dry-run are setup's layout flags (setup
	// --self-managed); with --node, --from is a directory holding the release (its
	// assets or an extracted archive) and --version defaults to the home's
	def("root", "", "install root (default per OS)")
	def("from", "", "with --node: a directory holding the release (its assets, or an extracted release)")
	def("version", "", "with --node: the release to install (default: the home's version)")
	defBool("dry-run", "print what would be done, change nothing")
	defBool("converting", "with --node: run by the rollout's update.convert (the release is this executable's)")
	def("python", "", "with --node: keep this Python interpreter for the pipeline instead of building one from the release's wheel")
	def("mode", "task", "with --node on Windows: task (logon task, default) or service")
	if fl.Lookup("instances") == nil {
		fl.Int("instances", -1, "with --node: extra transcription workers ([pipeline] instances; default: keep)")
	}
	defBool("no-service", "with --node: install the release but register no service")
	defBool("keep-worker", "with --node: leave the old worker units in place")
	_ = fl.MarkHidden("converting")
}

// nodeSetup is one run of setup --node (fields filled from the flags; tests set them).
type nodeSetup struct {
	Root, From, Version, Python, Mode string
	Instances                         int
	DryRun, Converting                bool
	NoService, KeepWorker             bool

	Out        io.Writer
	NodeConfig config.NodeConfig
	NodePath   string
	ConfigPath string // the node's server.toml
	Platform   string // os/arch
	Run        func(name string, args ...string) error
	HTTP       *http.Client
	Identity   releaseverify.Identity
	Trust      root.TrustedMaterial // nil: the embedded Sigstore root
	Healthy    func(ctx context.Context) error
	// InstallService writes and enables the service (service.Install; tests replace it).
	InstallService func(service.Spec) error
	Now            func() time.Time
	// CUDATorch reports whether this machine's NVIDIA GPU runs the second listen, so its
	// pipeline takes torch's CUDA build (nil: secondListenGPUHere; tests replace it).
	CUDATorch func() bool
	// GOOS, Home and SystemUnits are where the old worker's units are looked for (tests
	// set them): this OS, the user's home, and the system's unit folder when root on Linux
	// ("" otherwise).
	GOOS, Home, SystemUnits string
}

func (s *nodeSetup) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

func (s *nodeSetup) home() string {
	if s.Home != "" {
		return s.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

// units are this machine's Epic 31 worker units (worker_units.go).
func (s *nodeSetup) units() workerUnits {
	return workerUnits{goos: s.goos(), home: s.home(), systemUnits: s.SystemUnits, run: s.Run, say: s.say}
}

// workerInstances counts the extra workers (maktaba-node@1..N) the conversion keeps.
func (s *nodeSetup) workerInstances() int { return s.units().instances() }

// constraints is the pin set this machine's pipeline installs (story 38.9): the
// release's cuda set (torch's CUDA build, 2.0 GB) on Windows x86_64 where the NVIDIA
// GPU runs the second listen, else its default set (PyPI's torch, the CPU build on
// Windows). A release without the cuda set has only the default.
func (s *nodeSetup) constraints(p *releaseverify.Pipeline) string {
	if p.ConstraintsCUDA == "" || s.Platform != "windows/amd64" {
		return p.Constraints
	}
	cuda := s.CUDATorch
	if cuda == nil {
		cuda = secondListenGPUHere
	}
	if !cuda() {
		return p.Constraints
	}
	return p.ConstraintsCUDA
}

// secondListenGPUHere reports whether this machine's NVIDIA GPU runs the second listen
// (the supervisor's check: memory, compute capability, driver).
func secondListenGPUHere() bool {
	gpu, ok := supervisor.NvidiaGPU()
	return ok && supervisor.SecondListenGPU(gpu) == ""
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(v)
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func runSetupNodeCmd(cmd *cobra.Command, lf layoutFlags) error {
	inst, _ := cmd.Flags().GetInt("instances")
	id, err := releaseverify.IdentityFromEnv()
	if err != nil {
		return err
	}
	s := &nodeSetup{
		Root: orStr(lf.root, flagString(cmd, "root")), From: orStr(lf.from, flagString(cmd, "from")),
		Version: orStr(lf.version, flagString(cmd, "version")),
		Python:  flagString(cmd, "python"), Mode: flagString(cmd, "mode"), Instances: inst,
		DryRun: lf.dryRun || flagBool(cmd, "dry-run"), Converting: flagBool(cmd, "converting"),
		NoService: flagBool(cmd, "no-service"), KeepWorker: flagBool(cmd, "keep-worker"),
		Out: cmd.OutOrStdout(), NodeConfig: config.LocalNodeConfig(), NodePath: config.LocalNodeConfigPath(),
		ConfigPath: config.Path(), Platform: releaseverify.Platform(), Identity: id,
		HTTP: &http.Client{Timeout: 5 * time.Minute},
		Run: func(name string, args ...string) error {
			c := exec.Command(name, args...)
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			return c.Run()
		},
	}
	s.Healthy = func(ctx context.Context) error { return waitNodeAnswers(ctx, s.ConfigPath) }
	s.SystemUnits = rootSystemUnits() // a root install's worker units (install-node.sh --system)
	s.InstallService = func(sp service.Spec) error { return service.Install(sp, func(l string) { s.say("    %s", l) }) }
	return s.run(cmd.Context())
}

func (s *nodeSetup) say(format string, a ...any) { fmt.Fprintf(s.Out, format+"\n", a...) }

func (s *nodeSetup) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ErrRejoin: the node config predates handshake v2 (story 35.8 EC2).
var ErrRejoin = errors.New("this machine joined its home before the cluster channel's handshake v2 (node.json has no home.cluster_pub): re-join it (maktaba-pipeline join) and run setup --node again")

func (s *nodeSetup) run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	n := s.NodeConfig
	if n.Err != nil {
		return n.Err
	}
	if !n.Joined() || n.NodeID == "" {
		return fmt.Errorf("no joined node config at %s: join the home first (maktaba-pipeline join), then run setup --node", s.NodePath)
	}
	if strings.TrimSpace(n.Home.ClusterPub) == "" || n.HomeURL() == "" {
		return ErrRejoin
	}
	if s.Mode == "" {
		s.Mode = "task"
	}
	if s.Mode != "task" && s.Mode != "service" {
		return fmt.Errorf("--mode must be task or service")
	}
	if s.Root == "" {
		s.Root = layout.DefaultRoot(s.layoutMode())
	}
	s.say("Maktaba setup --node: %s (node %s) of the home at %s", hostName(), n.Node8(), n.HomeURL())
	s.say("  node config kept: %s", s.NodePath)

	// 1. the release
	if s.Converting && s.From == "" {
		if self, err := os.Executable(); err == nil {
			s.From = filepath.Dir(self)
		}
	}
	if s.Converting && s.Instances < 0 {
		// the rollout's conversion: the worker's extra instances become the node's
		if n := s.workerInstances(); n > 0 {
			s.Instances = n
		}
	}
	ver := strings.TrimPrefix(s.Version, "v")
	if ver == "" && s.From == "" {
		v, err := s.homeVersion(ctx)
		if err != nil {
			return fmt.Errorf("the home's version: %w (give --version or --from)", err)
		}
		ver = v
	}
	s.say("  release: %s (%s)", orStr(ver, "from "+s.From), s.Platform)
	if s.DryRun {
		s.plan(ver)
		return nil
	}
	rel, err := s.fetchVerified(ctx, ver)
	if err != nil {
		return fmt.Errorf("refusing the release: %w", err)
	}
	s.say("  verified %s: checksums.txt signed by %s", rel.tag, rel.identity)

	// 2. the layout
	dir, err := s.install(rel)
	if err != nil {
		return err
	}
	s.say("  installed %s", dir)

	// 3. the pipeline's environment
	python, err := s.pipelineEnv(ctx, rel, dir)
	if err != nil {
		return err
	}
	s.say("  pipeline: %s", python)

	// 4. server.toml
	if err := s.writeServerToml(); err != nil {
		return err
	}

	// 5. the service
	if s.NoService {
		s.say("  --no-service: start it with %s serve", filepath.Join(dir, exeName("maktaba-server")))
		return nil
	}
	if err := s.registerService(); err != nil {
		return err
	}
	// 6. healthy, then the old units go
	if s.Healthy != nil {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err := s.Healthy(hctx)
		cancel()
		if err != nil && s.Converting {
			// the rollout's conversion: the node's own start removes the worker units once it
			// runs (leftoverWorkerUnits), and the rollout waits for its agent on the release
			s.say("  the new service is not answering yet (%v): the node removes the old worker units when it starts", err)
			return nil
		}
		if err != nil {
			s.say("  the new service is not healthy yet (%v): the old worker units are kept; see %s", err, s.Root)
			return err
		}
	}
	if !s.KeepWorker {
		s.removeWorkerUnits()
	}
	s.say("Done: this machine is a full node. Its web UI: %s", s.webURL())
	return nil
}

// webURL is the node's web address as server.toml now has it.
func (s *nodeSetup) webURL() string {
	cfg, _, err := config.LoadFrom(s.ConfigPath)
	if err != nil {
		return "the address in " + s.ConfigPath
	}
	return cfg.PublicWebURL()
}

// layoutMode: --mode service is the system install (Windows service, system unit).
func (s *nodeSetup) layoutMode() layout.Mode {
	if s.Mode == "service" {
		return layout.ModeSystem
	}
	return layout.ModeUser
}

func (s *nodeSetup) plan(ver string) {
	s.say("  (--dry-run) would verify the release, then:")
	s.say("    install   %s", filepath.Join(s.Root, "bin", "releases", orStr(ver, "<version>")))
	s.say("    pointers  %s, %s, %s", filepath.Join(s.Root, "current"), filepath.Join(s.Root, "previous"), filepath.Join(s.Root, "watermark"))
	if s.Python != "" {
		s.say("    pipeline  keep %s", s.Python)
	} else {
		s.say("    pipeline  build %s from the release's wheel (--require-hashes)", filepath.Join(s.Root, "bin", "releases", orStr(ver, "<version>"), "venv"))
	}
	if s.Instances >= 0 {
		s.say("    config    %s [pipeline] instances = %d", s.ConfigPath, s.Instances)
	}
	switch {
	case s.NoService:
		s.say("    service   none (--no-service)")
	default:
		s.say("    service   %s (%s), running %s", service.DefaultKind(runtime.GOOS, s.layoutMode()), s.layoutMode(),
			filepath.Join(s.Root, exeName("maktaba-launcher")))
	}
	if !s.KeepWorker {
		s.say("    then      remove the worker units (maktaba-node, maktaba-node@N) once the node answers")
	}
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func hostName() string {
	h, _ := os.Hostname()
	return h
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// homeVersion asks the home's /api/system/version.
func (s *nodeSetup) homeVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.NodeConfig.HomeURL()+"/api/system/version", nil)
	if err != nil {
		return "", err
	}
	c := s.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&v); err != nil || v.Version == "" {
		return "", fmt.Errorf("no version in the home's answer")
	}
	return strings.TrimPrefix(v.Version, "v"), nil
}

// verifiedRelease is a release that passed verification, its files in memory.
type verifiedRelease struct {
	tag, version, identity string
	manifest               *releaseverify.Manifest
	files                  map[string][]byte // release assets kept beside the binaries
	binaries               map[string][]byte // archive entry → content, each checked against release.json
	constraints            string            // the pin set fetched (nodeSetup.constraints)
}

// fetchVerified gets the release (--from, the home's mirror, GitHub) and verifies it.
func (s *nodeSetup) fetchVerified(ctx context.Context, ver string) (*verifiedRelease, error) {
	get, err := s.source(ctx, ver)
	if err != nil {
		return nil, err
	}
	in := releaseverify.Input{Identity: s.Identity}
	for name, dst := range map[string]*[]byte{releaseverify.ChecksumsFile: &in.Checksums,
		releaseverify.BundleFile: &in.Bundle, releaseverify.ManifestFile: &in.Manifest} {
		b, err := get(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		*dst = b
	}
	var head struct {
		Tag     string `json:"tag"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(in.Manifest, &head)
	in.Tag = head.Tag
	if ver != "" && strings.TrimPrefix(head.Tag, "v") != ver {
		return nil, fmt.Errorf("the release found is %s, not v%s", head.Tag, ver)
	}
	in.TrustedMaterial = s.Trust
	res, err := releaseverify.Verify(in)
	if err != nil {
		return nil, err
	}
	rel := &verifiedRelease{tag: res.Manifest.Tag, version: res.Manifest.Version, identity: res.Identity,
		manifest: res.Manifest, files: map[string][]byte{releaseverify.ChecksumsFile: in.Checksums,
			releaseverify.BundleFile: in.Bundle, releaseverify.ManifestFile: in.Manifest}, binaries: map[string][]byte{}}
	asset, ok := res.Manifest.Assets[s.Platform]
	if !ok {
		return nil, fmt.Errorf("the release has no build for %s", s.Platform)
	}
	// an extracted release (the binaries beside release.json) or the platform archive
	names := make([]string, 0, len(asset.Files))
	for n := range asset.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	extracted := true
	for _, n := range names {
		b, err := get(n)
		if err != nil || b == nil {
			extracted = false
			break
		}
		if err := res.Manifest.VerifyExtracted(s.Platform, n, b); err != nil {
			return nil, err
		}
		rel.binaries[n] = b
	}
	if !extracted {
		archive, err := get(asset.Archive)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", asset.Archive, err)
		}
		if err := res.VerifyArtifact(releaseverify.Artifact{Name: asset.Archive, Data: archive}); err != nil {
			return nil, err
		}
		for _, n := range names {
			base := strings.TrimSuffix(n, ".exe")
			entry, b, err := releaseverify.ExtractBinary(asset.Archive, archive, base)
			if err != nil {
				return nil, fmt.Errorf("%s in %s: %w", n, asset.Archive, err)
			}
			if err := res.Manifest.VerifyExtracted(s.Platform, entry, b); err != nil {
				return nil, err
			}
			// kept under the manifest's own name, never the archive's path
			rel.binaries[n] = b
		}
	}
	// the pipeline's wheel and constraints, when the release has them and they are found
	if p := res.Manifest.Pipeline; p != nil && s.Python == "" {
		rel.constraints = s.constraints(p)
		for _, name := range []string{p.Wheel, rel.constraints} {
			b, err := get(name)
			if err != nil || b == nil {
				return nil, fmt.Errorf("%s: not found (give --python to keep the worker's interpreter)", name)
			}
			if err := res.VerifyArtifact(releaseverify.Artifact{Name: name, Data: b}); err != nil {
				return nil, err
			}
			if p.IsConstraints(name) {
				if err := releaseverify.CheckConstraints(name, b); err != nil {
					return nil, err
				}
			}
			rel.files[name] = b
		}
	}
	return rel, nil
}

// source returns a getter of release files by name.
func (s *nodeSetup) source(ctx context.Context, ver string) (func(string) ([]byte, error), error) {
	if s.From != "" {
		dir := s.From
		return func(name string) ([]byte, error) {
			if strings.ContainsAny(name, `/\`) {
				return nil, fmt.Errorf("bad name %q", name)
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return b, err
		}, nil
	}
	c := s.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	download := func(u string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/octet-stream")
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s answered %d", u, resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	}
	// the home's artifact mirror (story 35.12) first, GitHub Releases otherwise
	mirror := s.NodeConfig.HomeURL() + "/api/cluster/updates/artifacts/" + ver + "/"
	if b, err := download(mirror + releaseverify.ManifestFile); err == nil && b != nil {
		s.say("  source: the home's mirror")
		return func(name string) ([]byte, error) { return download(mirror + name) }, nil
	}
	gh := selfupdate.New(selfupdate.Options{Identity: s.Identity, Token: os.Getenv("MAKTABA_GITHUB_TOKEN")})
	rel, err := gh.ByTag(ctx, "v"+ver)
	if err != nil {
		return nil, fmt.Errorf("release v%s on GitHub: %w", ver, err)
	}
	s.say("  source: GitHub Releases (%s)", rel.Tag)
	return func(name string) ([]byte, error) {
		u, ok := rel.Assets[name]
		if !ok {
			return nil, nil
		}
		return download(u)
	}, nil
}

// install stages the verified release as `setup --self-managed` does
// (selfmanaged.Convert: bin/releases/<v>/ with .verified, the pointers, the
// launcher), from a staging copy of the verified bytes, plus the release files
// (release.json, checksums.txt, its bundle) the launcher's re-check reads.
func (s *nodeSetup) install(rel *verifiedRelease) (string, error) {
	stage, err := os.MkdirTemp("", "maktaba-node-release-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	for n, b := range rel.binaries {
		if !safeFileName(n) {
			return "", fmt.Errorf("refusing the file name %q in release.json", n)
		}
		if err := writeAtomic(filepath.Join(stage, n), b, 0o755); err != nil {
			return "", err
		}
	}
	res, err := selfmanaged.Convert(selfmanaged.Options{Root: s.Root, From: stage, Version: rel.version,
		Identity: rel.identity, Now: s.now(), Log: func(l string) { s.say("    %s", l) }})
	if err != nil {
		return "", err
	}
	l := layout.New(s.Root)
	dir := l.ReleaseDir(res.Version)
	for n, b := range rel.files {
		if !safeFileName(n) {
			return "", fmt.Errorf("refusing the file name %q in release.json", n)
		}
		if err := writeAtomic(filepath.Join(dir, n), b, 0o644); err != nil {
			return "", err
		}
	}
	if res.Skipped != "" && res.Skipped != "already current" {
		return "", fmt.Errorf("the release was staged but current was not switched: %s", res.Skipped)
	}
	return dir, nil
}

// safeFileName accepts a plain file name: no directory part, no "..".
func safeFileName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, `/\:`) && filepath.Base(n) == n
}

func readPointer(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// pipelineEnv builds <release>/venv from the release's wheel and hash-checked
// constraints (design §3.4), or keeps --python.
func (s *nodeSetup) pipelineEnv(_ context.Context, rel *verifiedRelease, dir string) (string, error) {
	if s.Python != "" {
		return s.Python, nil
	}
	p := rel.manifest.Pipeline
	if p == nil {
		return "", errors.New("the release has no pipeline wheel: give --python (the worker's interpreter)")
	}
	venv := filepath.Join(dir, "venv")
	py := filepath.Join(venv, "bin", "python")
	if runtime.GOOS == "windows" {
		py = filepath.Join(venv, "Scripts", "python.exe")
	}
	base := "python3"
	if runtime.GOOS == "windows" {
		base = "python"
	}
	if err := s.Run(base, "-m", "venv", venv); err != nil {
		return "", fmt.Errorf("create the pipeline's environment: %w", err)
	}
	cons := rel.constraints
	if cons == "" {
		cons = p.Constraints
	}
	if err := s.Run(py, "-m", "pip", "install", "--require-hashes", "--no-deps", "-r", filepath.Join(dir, cons)); err != nil {
		return "", fmt.Errorf("install the pipeline's dependencies (--require-hashes): %w", err)
	}
	if err := s.Run(py, "-m", "pip", "install", "--no-deps", filepath.Join(dir, p.Wheel)); err != nil {
		return "", fmt.Errorf("install the pipeline: %w", err)
	}
	return py, nil
}

// writeServerToml sets [pipeline] instances in the node's server.toml (creating a
// node server.toml when there is none), keeping everything else.
func (s *nodeSetup) writeServerToml() error {
	cfg, _, err := config.LoadFrom(s.ConfigPath)
	if err != nil {
		return err
	}
	changed := false
	if s.Instances >= 0 && (!cfg.PipelineInstancesSet || cfg.Pipeline.Instances != s.Instances) {
		cfg.Pipeline.Instances, cfg.PipelineInstancesSet = s.Instances, true // a 0 is kept too
		changed = true
	}
	if s.Python != "" && cfg.Pipeline.Python != s.Python {
		cfg.Pipeline.Python = s.Python
		changed = true
	}
	if _, err := os.Stat(s.ConfigPath); err != nil {
		changed = true
	}
	// the web, API and streaming ports: one another program holds (Splunk's 8088,
	// 8080 on many Windows desktops) moves into the fallback range (config/ports.go)
	moved, err := reconcilePorts(&cfg, s.ConfigPath, config.PortOptions{PreferWeb: nodeWebPreference(), KeepOurs: true},
		s.Out, false)
	if err != nil {
		return err
	}
	if len(moved) > 0 {
		changed = true
	}
	if !changed {
		return nil
	}
	s.say("  config: %s", s.ConfigPath)
	return cfg.SaveTo(s.ConfigPath)
}

// registerService writes the launcher's service as `setup --service` does and
// starts it.
func (s *nodeSetup) registerService() error {
	mode := s.layoutMode()
	spec := service.Spec{Kind: service.DefaultKind(runtime.GOOS, mode), Mode: mode, Root: s.Root,
		ConfigPath: s.ConfigPath}
	s.say("  service: %s (%s)", spec.Kind, mode)
	if err := s.InstallService(spec); err != nil {
		return fmt.Errorf("register the service: %w", err)
	}
	switch spec.Kind {
	case service.KindTask:
		return s.Run("schtasks", "/Run", "/TN", service.WindowsName)
	case service.KindWinService:
		return s.Run("sc", "start", service.WindowsName)
	case service.KindSystemd:
		if mode == layout.ModeSystem {
			return s.Run("systemctl", "restart", service.Name)
		}
		return s.Run("systemctl", "--user", "restart", service.Name)
	}
	return nil // launchd: bootstrap started it
}

// removeWorkerUnits removes the Epic 31 installer's worker units (only after the new
// service answered).
func (s *nodeSetup) removeWorkerUnits() { s.units().remove() }

// waitNodeAnswers waits until this machine's API answers /api/system/version as a node.
func waitNodeAnswers(ctx context.Context, configPath string) error {
	cfg, _, err := config.LoadFrom(configPath)
	if err != nil {
		return err
	}
	u := apiOrigin(cfg.Server.Listen)
	if u == nil {
		return errors.New("no API address in server.toml")
	}
	c := &http.Client{Timeout: 3 * time.Second}
	for {
		if resp, err := c.Get(u.String() + "/api/system/version"); err == nil {
			var v struct {
				Role string `json:"role"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&v)
			_ = resp.Body.Close()
			if v.Role == "node" {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the node's API did not answer at %s: %w", u, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
