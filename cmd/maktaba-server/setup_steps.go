package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// setupPending is what the setup steps collected, until `finish` applies it
// (<state>/state/setup.json, 0600). It holds no secret in the clear: the
// admin's password only as its argon2id hash, the Hugging Face and Plex
// tokens in files of their own in the keys and models folders.
type setupPending struct {
	Admin   *pendingAdmin  `json:"admin,omitempty"`
	Roots   []string       `json:"roots,omitempty"`
	PathMap string         `json:"path_map,omitempty"`
	Models  *pendingModels `json:"models,omitempty"`
	Plex    *pendingPlex   `json:"plex,omitempty"`
}

type pendingAdmin struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type pendingModels struct {
	Source string `json:"source"` // huggingface | nas
	Path   string `json:"path,omitempty"`
}

type pendingPlex struct {
	ClientID string `json:"client_id"`
	Linked   bool   `json:"linked"`
}

func (s *setupRun) pendingPath() string {
	return filepath.Join(s.cfg.StateDir(), "state", "setup.json")
}

func (s *setupRun) loadPending() (setupPending, error) {
	var p setupPending
	raw, err := os.ReadFile(s.pendingPath())
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return setupPending{}, fmt.Errorf("%s: %w", s.pendingPath(), err)
	}
	return p, nil
}

func (s *setupRun) savePending(p setupPending) error {
	path := s.pendingPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(path, raw, 0o600)
}

// updatePending changes the pending state with fn and saves it.
func (s *setupRun) updatePending(fn func(*setupPending)) error {
	p, err := s.loadPending()
	if err != nil {
		return err
	}
	fn(&p)
	return s.savePending(p)
}

// --- check ---------------------------------------------------------------------

const (
	setupNeedRAM  = 8 << 30  // what the server and one transcription worker need
	setupNeedDisk = 20 << 30 // the venv, the models, the database to start with
	setupMinRAM   = 4 << 30  // below this the pipeline cannot run beside the rest
)

type setupNeed struct {
	RAMBytes  uint64 `json:"ram_bytes"`
	DiskBytes uint64 `json:"disk_bytes"`
}

// setupRole is one line of what will run: the role (the screens' text is
// setup.check.run.<role>, with params) and its English.
type setupRole struct {
	Role   string         `json:"role"`
	What   string         `json:"what"`
	Params map[string]any `json:"params,omitempty"`
}

// checkNote is one finding of check: a code the screens translate
// (setup.check.note.<code>, with params; a size is in bytes under a *_bytes
// name) and its English, for a log or a screen that does not know the code.
type checkNote struct {
	Code    string         `json:"code"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`
}

type checkResult struct {
	Platform      string    `json:"platform"`
	Arch          string    `json:"arch"`
	RAMBytes      uint64    `json:"ram_bytes"`
	DiskFreeBytes uint64    `json:"disk_free_bytes"`
	DataDir       string    `json:"data_dir"`
	Need          setupNeed `json:"need"`
	// OK: no problem, so the screen's Next is allowed.
	OK bool `json:"ok"`
	// Problems are why this machine cannot run the server (they block Next);
	// Warnings are worth knowing and block nothing.
	Problems []checkNote `json:"problems"`
	Warnings []checkNote `json:"warnings"`
	// GPUs are the NVIDIA GPUs transcription can use (Windows and Linux).
	GPUs []hostinfo.GPU `json:"gpus"`
	Runs []setupRole    `json:"runs"`
}

func newNote(code, message string, kv ...any) checkNote {
	n := checkNote{Code: code, Message: message}
	for i := 0; i+1 < len(kv); i += 2 {
		if n.Params == nil {
			n.Params = map[string]any{}
		}
		n.Params[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return n
}

// Problem records why this machine cannot run the server.
func (r *checkResult) Problem(code, message string, kv ...any) {
	r.Problems = append(r.Problems, newNote(code, message, kv...))
	r.OK = false
}

// Warn records what is worth knowing.
func (r *checkResult) Warn(code, message string, kv ...any) {
	r.Warnings = append(r.Warnings, newNote(code, message, kv...))
}

// platformChecks are what a platform adds to check (a Linux file adds glibc,
// the systemd user manager and linger, Epic 39): each reads this machine and
// records its problems and warnings.
var platformChecks []func(ctx context.Context, res *checkResult)

// hostFacts are what check reads about this machine (tests replace it).
var hostFacts = func(dir string) (ram, disk uint64) {
	return hostinfo.TotalMemory(), hostinfo.FreeDisk(dir)
}

// hostGPUs are the NVIDIA GPUs check reports (tests replace it).
var hostGPUs = hostinfo.NVIDIA

// setupPlatform says why the app cannot run the server here (code and its
// English), or nothing: the desktop app's server payload is built for Apple
// Silicon (MLX) and x86_64 Windows, and Linux on x86_64 and arm64.
func setupPlatform(goos, arch string) (code, message string) {
	switch goos + "/" + arch {
	case "darwin/arm64", "windows/amd64", "linux/amd64", "linux/arm64":
		return "", ""
	case "darwin/amd64":
		return "platform_intel_mac", "This Mac has an Intel processor: the Maktaba app runs the server on Apple Silicon only."
	}
	return "platform_unsupported", fmt.Sprintf("Maktaba does not run its server on %s/%s.", goos, arch)
}

// stepCheck: what will run, and whether this machine has the memory and the
// disk for it. A problem blocks the screen's Next; a warning does not.
func stepCheck(s *setupRun, _ json.RawMessage) (any, error) {
	root := s.cfg.StateDir()
	ram, disk := hostFacts(root)
	res := checkResult{
		Platform: runtime.GOOS, Arch: runtime.GOARCH,
		RAMBytes: ram, DiskFreeBytes: disk, DataDir: root,
		Need:     setupNeed{RAMBytes: setupNeedRAM, DiskBytes: setupNeedDisk},
		OK:       true,
		Problems: []checkNote{},
		Warnings: []checkNote{},
		GPUs:     []hostinfo.GPU{},
	}
	if code, msg := setupPlatform(runtime.GOOS, runtime.GOARCH); code != "" {
		res.Problem(code, msg, "os", runtime.GOOS, "arch", runtime.GOARCH)
	}
	switch {
	case disk > 0 && disk < setupNeedDisk:
		res.Problem("disk_low", fmt.Sprintf("Only %s free on the disk of %s: Maktaba needs %s to start.",
			humanBytes(disk), root, humanBytes(setupNeedDisk)), "free_bytes", disk, "need_bytes", setupNeedDisk, "dir", root)
	case disk > 0 && disk < 2*setupNeedDisk:
		res.Warn("disk_tight", "Less than 40 GB free: the database, the models and the caches grow with the library.",
			"free_bytes", disk, "comfortable_bytes", uint64(2*setupNeedDisk))
	}
	switch {
	case ram > 0 && ram < setupMinRAM:
		res.Problem("ram_low", fmt.Sprintf("This machine has %s of memory: Maktaba needs at least %s.",
			humanBytes(ram), humanBytes(setupMinRAM)), "have_bytes", ram, "need_bytes", setupMinRAM)
	case ram > 0 && ram < setupNeedRAM:
		res.Warn("ram_tight", "Less than 8 GB of memory: indexing and transcription will be slow.", "need_bytes", setupNeedRAM)
	}
	// transcription on Windows and Linux: an NVIDIA GPU (CUDA), else the processor
	gpu := ""
	if runtime.GOOS != "darwin" {
		if gpus := hostGPUs(); len(gpus) > 0 {
			res.GPUs, gpu = gpus, gpus[0].Name
		} else {
			res.Warn("transcribe_cpu", "No NVIDIA GPU: transcription runs on the processor, many times slower than on a GPU.")
		}
	}
	for _, check := range platformChecks {
		check(s.ctx, &res)
	}

	exe, _ := os.Executable()
	if _, ok, err := pgbundle.Resolve(s.cfg, exe); err == nil && ok {
		res.Runs = append(res.Runs, setupRole{Role: "postgres", What: "The library database (private to Maktaba)"})
	}
	workers := supervisor.PlanSTT(s.cfg).Workers
	pipeline := setupRole{Role: "pipeline", What: pipelineWhat(workers, gpu), Params: map[string]any{"workers": workers}}
	if gpu != "" {
		pipeline.Params["gpu"] = gpu
	}
	res.Runs = append(res.Runs,
		setupRole{Role: "api", What: "The server the app and your devices talk to"},
		setupRole{Role: "streaming", What: "Playback and Live TV"},
		pipeline,
	)
	return res, nil
}

func pipelineWhat(workers int, gpu string) string {
	what := "Indexing, transcription and search"
	switch {
	case workers == 1:
		what = "Indexing, transcription (1 worker) and search"
	case workers > 1:
		what = fmt.Sprintf("Indexing, transcription (%d workers) and search", workers)
	}
	if gpu != "" {
		what += ", transcribing on " + gpu
	}
	return what
}

func humanBytes(n uint64) string {
	const gb = 1 << 30
	if n >= gb {
		return fmt.Sprintf("%.0f GB", float64(n)/gb)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
}

// --- admin ---------------------------------------------------------------------

// the api's own limits (users.validateUsername, argon2id's MaxLen; the CLI
// wizard's minimum length)
const (
	adminMinPassword = 8
	adminMaxPassword = 256
)

const adminMaxUsername = 64

func validAdminName(u string) error {
	if u == "" || len(u) > adminMaxUsername {
		return failStep("username_length", "username", "A username is 1 to %d characters.", adminMaxUsername).with("max", adminMaxUsername)
	}
	if strings.TrimSpace(u) != u {
		return failStep("username_space", "username", "A username cannot start or end with a space.")
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f {
			return failStep("username_control", "username", "A username cannot hold control characters.")
		}
	}
	return nil
}

// validAdminPassword: the api's own limits, and one line (the api reads the
// password as a line: a line break would cut it there).
func validAdminPassword(pw string) error {
	switch {
	case strings.ContainsAny(pw, "\r\n"):
		return failStep("password_newline", "password", "A password cannot hold a line break.")
	case len([]rune(pw)) < adminMinPassword:
		return failStep("password_short", "password", "Use at least %d characters.", adminMinPassword).with("min", adminMinPassword)
	case len(pw) > adminMaxPassword:
		return failStep("password_long", "password", "Use at most %d characters.", adminMaxPassword).with("max", adminMaxPassword)
	}
	return nil
}

// stepAdmin: the first account. The password is hashed here, by the api
// (`maktaba-api hash-password`, the same argon2id every user gets), and only
// its hash waits in the pending state until `finish` seeds the account.
func stepAdmin(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	if err := validAdminName(req.Username); err != nil {
		return nil, err
	}
	if err := validAdminPassword(req.Password); err != nil {
		return nil, err
	}
	out, _, err := s.apiSub("", strings.NewReader(req.Password+"\n"), "hash-password")
	if err != nil {
		var se *stepError
		if errors.As(err, &se) {
			return nil, err
		}
		return nil, fmt.Errorf("hash the password: %w", err)
	}
	hash := strings.TrimSpace(string(out))
	if !strings.HasPrefix(hash, "$argon2id$") {
		return nil, errors.New("hash the password: the api gave no argon2id hash")
	}
	if err := s.updatePending(func(p *setupPending) {
		p.Admin = &pendingAdmin{Username: req.Username, PasswordHash: hash}
	}); err != nil {
		return nil, err
	}
	return map[string]string{"username": req.Username}, nil
}

// --- libraries -----------------------------------------------------------------

type rootCheck struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Readable bool   `json:"readable"`
}

// localPath is a path of this machine, whole: a drive letter's folder (a bare
// "Y:" is the drive's root), a UNC path, or (elsewhere) a path from /; ok
// false for a relative one, which would resolve against wherever the server
// happens to run.
func localPath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if runtime.GOOS == "windows" && len(p) == 2 && p[1] == ':' {
		p += `\`
	}
	if !filepath.IsAbs(p) {
		return p, false
	}
	return filepath.Clean(p), true
}

// otherPath: a whole path in any machine's form (the path map's left side is
// another machine's: /Volumes/... on a Mac, Y:/... or \\NAS\... on Windows).
func otherPath(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) ||
		(len(p) >= 2 && p[1] == ':' && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z'))
}

// samePath: two folders of this machine are one (Windows and the Mac's disks
// ignore case; Linux's do not).
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// stepLibraries: the library folders (each must be a whole path that exists
// and is readable here: a drive letter, a UNC path or a mounted share) and the
// NAS path map (the paths other machines report → this machine's, [media]
// path_map), both sides whole paths.
func stepLibraries(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		Roots   []string `json:"roots"`
		PathMap []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"path_map"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	var roots []string
	var checks []rootCheck
	for _, r := range req.Roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		clean, ok := localPath(r)
		if !ok {
			return nil, failStep("root_relative", "roots", "%s is not a whole path (it starts at a drive or at /).", clean).with("path", clean)
		}
		dup := false
		for _, seen := range roots {
			dup = dup || samePath(seen, clean)
		}
		if dup {
			continue
		}
		c := rootCheck{Path: clean}
		info, err := os.Stat(clean)
		c.Exists = err == nil && info.IsDir()
		if c.Exists {
			_, err := os.ReadDir(clean)
			c.Readable = err == nil
		}
		checks = append(checks, c)
		switch {
		case !c.Exists:
			return nil, failStep("root_missing", "roots", "%s is not a folder on this machine.", clean).with("path", clean)
		case !c.Readable:
			return nil, failStep("root_unreadable", "roots", "Maktaba cannot read %s.", clean).with("path", clean)
		}
		roots = append(roots, clean)
	}
	if len(roots) == 0 {
		return nil, failStep("roots_none", "roots", "Give at least one library folder.")
	}
	m := map[string]string{}
	for i, e := range req.PathMap {
		from, to := strings.TrimSpace(e.From), strings.TrimSpace(e.To)
		line := i + 1
		if from == "" || to == "" {
			return nil, failStep("bad_path_map", "path_map", "Line %d of the path map needs both paths.", line).with("line", line)
		}
		if !otherPath(from) {
			return nil, failStep("path_map_relative", "path_map", "Line %d: %s is not a whole path.", line, from).
				with("line", line, "path", from)
		}
		here, ok := localPath(to)
		if !ok {
			return nil, failStep("path_map_relative", "path_map", "Line %d: %s is not a whole path.", line, to).
				with("line", line, "path", to)
		}
		if strings.HasSuffix(to, "/") || strings.HasSuffix(to, `\`) {
			here = strings.TrimRight(here, `/\`) + string(filepath.Separator) // a prefix keeps its separator
		}
		m[from] = here
	}
	pathMap := ""
	if len(m) > 0 {
		// the JSON form of MAKTABA_PATH_MAP: no separator a path can hold
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		pathMap = string(raw)
	}
	if err := s.updatePending(func(p *setupPending) {
		p.Roots, p.PathMap = roots, pathMap
	}); err != nil {
		return nil, err
	}
	return map[string]any{"roots": checks}, nil
}
