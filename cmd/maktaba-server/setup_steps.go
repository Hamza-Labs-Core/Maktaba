package main

import (
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

type setupRole struct {
	Role string `json:"role"`
	What string `json:"what"`
}

type checkResult struct {
	Platform      string      `json:"platform"`
	Arch          string      `json:"arch"`
	RAMBytes      uint64      `json:"ram_bytes"`
	DiskFreeBytes uint64      `json:"disk_free_bytes"`
	DataDir       string      `json:"data_dir"`
	Need          setupNeed   `json:"need"`
	OK            bool        `json:"ok"`
	Warnings      []string    `json:"warnings"`
	Runs          []setupRole `json:"runs"`
}

// hostFacts are what check reads about this machine (tests replace it).
var hostFacts = func(dir string) (ram, disk uint64) {
	return hostinfo.TotalMemory(), hostinfo.FreeDisk(dir)
}

// setupPlatform reports whether the app can run the server here: the desktop
// app's server payload is built for Apple Silicon (MLX) and x86_64 Windows,
// and Linux on x86_64 and arm64.
func setupPlatform(goos, arch string) (bool, string) {
	switch goos + "/" + arch {
	case "darwin/arm64", "windows/amd64", "linux/amd64", "linux/arm64":
		return true, ""
	case "darwin/amd64":
		return false, "This Mac has an Intel processor: the Maktaba app runs the server on Apple Silicon only."
	}
	return false, fmt.Sprintf("Maktaba does not run its server on %s/%s.", goos, arch)
}

// stepCheck: what will run, and whether this machine has the memory and the
// disk for it. ok false blocks the screen's Next; warnings do not.
func stepCheck(s *setupRun, _ json.RawMessage) (any, error) {
	root := s.cfg.StateDir()
	ram, disk := hostFacts(root)
	res := checkResult{
		Platform: runtime.GOOS, Arch: runtime.GOARCH,
		RAMBytes: ram, DiskFreeBytes: disk, DataDir: root,
		Need:     setupNeed{RAMBytes: setupNeedRAM, DiskBytes: setupNeedDisk},
		OK:       true,
		Warnings: []string{},
	}
	if ok, why := setupPlatform(runtime.GOOS, runtime.GOARCH); !ok {
		res.OK = false
		res.Warnings = append(res.Warnings, why)
	}
	switch {
	case disk > 0 && disk < setupNeedDisk:
		res.OK = false
		res.Warnings = append(res.Warnings, fmt.Sprintf("Only %s free on the disk of %s: Maktaba needs %s to start.",
			humanBytes(disk), root, humanBytes(setupNeedDisk)))
	case disk > 0 && disk < 2*setupNeedDisk:
		res.Warnings = append(res.Warnings, "Less than 40 GB free: the database, the models and the caches grow with the library.")
	}
	switch {
	case ram > 0 && ram < setupMinRAM:
		res.OK = false
		res.Warnings = append(res.Warnings, fmt.Sprintf("This machine has %s of memory: Maktaba needs at least %s.",
			humanBytes(ram), humanBytes(setupMinRAM)))
	case ram > 0 && ram < setupNeedRAM:
		res.Warnings = append(res.Warnings, "Less than 8 GB of memory: indexing and transcription will be slow.")
	}
	platformCheck(s.ctx, &res) // what is particular to this OS (setup_check_*.go)

	exe, _ := os.Executable()
	if _, ok, err := pgbundle.Resolve(s.cfg, exe); err == nil && ok {
		res.Runs = append(res.Runs, setupRole{"postgres", "The library database (private to Maktaba)"})
	}
	res.Runs = append(res.Runs,
		setupRole{"api", "The server the app and your devices talk to"},
		setupRole{"streaming", "Playback and Live TV"},
		setupRole{"pipeline", pipelineWhat(supervisor.PlanSTT(s.cfg).Workers)},
	)
	return res, nil
}

func pipelineWhat(workers int) string {
	switch workers {
	case 0:
		return "Indexing, transcription and search"
	case 1:
		return "Indexing, transcription (1 worker) and search"
	}
	return fmt.Sprintf("Indexing, transcription (%d workers) and search", workers)
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

func validAdminName(u string) error {
	if u == "" || len(u) > 64 {
		return failStep("bad_username", "username", "A username is 1 to 64 characters.")
	}
	if strings.TrimSpace(u) != u {
		return failStep("bad_username", "username", "A username cannot start or end with a space.")
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f {
			return failStep("bad_username", "username", "A username cannot hold control characters.")
		}
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
	switch n := len([]rune(req.Password)); {
	case n < adminMinPassword:
		return nil, failStep("weak_password", "password", "Use at least %d characters.", adminMinPassword)
	case len(req.Password) > adminMaxPassword:
		return nil, failStep("weak_password", "password", "Use at most %d characters.", adminMaxPassword)
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

// stepLibraries: the library folders (each must exist and be readable here:
// a drive letter, a UNC path or a mounted share) and the NAS path map (the
// paths other machines report → this machine's, [media] path_map).
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
	seen := map[string]bool{}
	for _, r := range req.Roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		clean := filepath.Clean(r)
		if seen[strings.ToLower(clean)] {
			continue
		}
		seen[strings.ToLower(clean)] = true
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
			return nil, failStep("root_missing", "roots", "%s is not a folder on this machine.", clean)
		case !c.Readable:
			return nil, failStep("root_unreadable", "roots", "Maktaba cannot read %s.", clean)
		}
		roots = append(roots, clean)
	}
	if len(roots) == 0 {
		return nil, failStep("root_missing", "roots", "Give at least one library folder.")
	}
	m := map[string]string{}
	for _, e := range req.PathMap {
		from, to := strings.TrimSpace(e.From), strings.TrimSpace(e.To)
		if from == "" || to == "" {
			return nil, failStep("bad_path_map", "path_map", "Each line needs both paths.")
		}
		m[from] = to
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
