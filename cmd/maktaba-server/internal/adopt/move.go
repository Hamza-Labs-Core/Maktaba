package adopt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/devmigrate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Target is the app the install moves into.
type Target struct {
	ConfigPath string // the app's server.toml
	StateDir   string // the app's state root (moved/, chroma/, models/, cache/)
	KeysDir    string
	NodeConfig string // where the app reads node.json
	// ModeFile is the app window's mode.json (the shell's mode.rs; on Windows
	// %LOCALAPPDATA%\com.hamzalabs.maktaba): Undo takes its server mode out.
	ModeFile string
	// AppPayload is the app's own install (the payload the setup step runs
	// from): its programs, and this process, are never stopped as the old
	// install's.
	AppPayload string
}

// pausedFlag is the shell's pause flag (agent.rs): a paused server is not
// started by the app until the owner resumes it. On Windows it holds in the
// sign-in it was set in (one without a sign-in line: written after this
// sign-in began); the next sign-in ends it.
func (t Target) pausedFlag() string { return filepath.Join(t.StateDir, "state", "server-paused") }

// self is the app's own side: its install, its pipeline venv, this process.
func (t Target) self() self {
	venv := ""
	if t.StateDir != "" {
		venv = filepath.Join(t.StateDir, "venv")
	}
	return selfOf(t.AppPayload, venv)
}

// The stages of a move, in its record (moved/move.json).
const (
	StageFound    = "found"    // under way: the old install still runs
	StageStopped  = "stopped"  // the old install is stopped and its task disabled
	StageMoved    = "moved"    // its settings and files are in the app
	StageVerified = "verified" // the app's server runs with them
	StageFinished = "finished" // the old task is removed (its XML stays)
	StageUndone   = "undone"   // the old install runs again
)

// Record is what a move did, in <state>/moved/move.json.
type Record struct {
	Stage      string     `json:"stage"`
	Source     Source     `json:"source"`
	Saved      []Saved    `json:"saved_tasks"` // the old tasks' definitions
	Carried    []Note     `json:"carried"`
	NotCarried []Note     `json:"not_carried"`
	Copied     []string   `json:"copied"`
	StartedAt  time.Time  `json:"started_at"`
	MovedAt    *time.Time `json:"moved_at,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	UndoneAt   *time.Time `json:"undone_at,omitempty"`
}

// Saved is an old task's definition, kept in moved/ (Undo registers it again).
type Saved struct {
	Task string `json:"task"`
	XML  string `json:"xml"`
}

// RecordPath is the move's record.
func RecordPath(stateDir string) string { return filepath.Join(stateDir, "moved", "move.json") }

// ReadRecord reads the move's record (ok false when this machine moved nothing).
func ReadRecord(stateDir string) (Record, bool, error) {
	var r Record
	raw, err := os.ReadFile(RecordPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	return r, true, json.Unmarshal(raw, &r)
}

func writeRecord(stateDir string, r Record) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(RecordPath(stateDir)), 0o700); err != nil {
		return err
	}
	return layout.WriteFileAtomic(RecordPath(stateDir), append(raw, '\n'), 0o600)
}

func now() *time.Time {
	t := time.Now().UTC()
	return &t
}

// Item is one file or folder the move copies into the app: Code (and Params)
// name it for the screens (setup.move.item.<code>), What in English.
type Item struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
	What   string         `json:"what"`
	From   string         `json:"from"`
	To     string         `json:"to"`
	Size   int64          `json:"size"`
	Done   bool           `json:"done"` // already in the app, the same (left as it is)
	item   devmigrate.Item
}

// Plan is what a move of one source would do, read without changing anything.
// Carried and NotCarried are the settings (setup.move.carry.<code>,
// setup.move.nocarry.<code>).
type Plan struct {
	Source     Source `json:"source"`
	Items      []Item `json:"items"`
	Carried    []Note `json:"carried"`
	NotCarried []Note `json:"not_carried"`
	// Problems stop the move (nothing is changed while any is left).
	Problems []Note `json:"problems"`
	Warnings []Note `json:"warnings"`
	Need     int64  `json:"need"` // bytes the copies take
	Free     int64  `json:"free"` // free bytes where the app keeps its state (-1 unknown)

	config config.Config // the app's server.toml after the move
}

// MakePlan reads what moving src into tgt does.
func MakePlan(sys Sys, src Source, tgt Target) (Plan, error) {
	p := Plan{Source: src, Items: []Item{}, Carried: []Note{}, NotCarried: []Note{}, Problems: []Note{}, Warnings: []Note{}, Free: -1}
	if src.Kind == KindService {
		// the system's account runs it: the user's app can neither stop it nor
		// read its settings
		p.Problems = append(p.Problems, note("service_install",
			"it runs as the Windows service "+src.Service+", under the system's account: only an administrator can stop it, so the app cannot move it",
			"name", src.Service))
		return p, nil
	}
	old := config.Defaults()
	if src.ConfigFound {
		c, _, err := config.LoadFrom(src.Config)
		if err != nil {
			return p, err
		}
		old = c
	}
	cfg, carried, notCarried, err := carrySettings(src, old, tgt)
	if err != nil {
		return p, err
	}
	p.config, p.Carried, p.NotCarried = cfg, carried, notCarried
	p.Items = moveItems(src, old, cfg, tgt, src.getenv(sys))
	for _, it := range p.Items {
		if it.item.Key && it.Done && !sameBytes(it.From, it.To) {
			// never replaced: found now, before anything is stopped
			p.Problems = append(p.Problems, note("key_differs",
				fmt.Sprintf("the app has another %s at %s than the old install's %s: a key is never replaced, so it cannot move in", it.What, it.To, it.From),
				"path", it.To, "from", it.From))
		}
		if !it.Done {
			p.Need += it.Size
		}
	}
	if free := hostinfo.FreeDisk(existingParent(tgt.StateDir)); free > 0 {
		p.Free = int64(free)
		if p.Need > p.Free {
			p.Problems = append(p.Problems, note("space_low",
				fmt.Sprintf("the copies need %s and %s is free where the app keeps its files", human(p.Need), human(p.Free)),
				"need_bytes", p.Need, "free_bytes", p.Free))
		}
	}
	switch src.DB {
	case DBNode:
		if src.Kind == KindNodeWorker && src.Home == "" {
			p.Problems = append(p.Problems, note("worker_not_joined",
				"this worker has not joined a home (no node config at "+src.NodeConfig+")", "path", src.NodeConfig))
		}
	case DBOwnPostgres:
		if _, port, _ := net.SplitHostPort(src.DBWhere); port != "" && !sys.Listening(port) {
			p.Problems = append(p.Problems, note("postgres_down",
				"its library database, the Postgres at "+src.DBWhere+", is not running: the move needs it running",
				"where", src.DBWhere))
		}
		p.Warnings = append(p.Warnings, note("postgres_stays",
			"the library database stays in the Postgres at "+src.DBWhere+" on this PC, which keeps running as it does now",
			"where", src.DBWhere))
	}
	if src.Kind == KindProcess {
		p.Warnings = append(p.Warnings, note("started_by_hand", "it was started without a task: the move ends it"))
	}
	return p, nil
}

// ProblemsText is the plan's problems in English, on one line.
func (p Plan) ProblemsText() string {
	parts := make([]string, len(p.Problems))
	for i, n := range p.Problems {
		parts[i] = n.Message
	}
	return strings.Join(parts, "; ")
}

// moveItems are the files the app takes over: the keys and the node config
// (never replaced when the app has a different one), the search index, the
// streaming cache, the models, the Hugging Face sign-in and an SQLite database.
func moveItems(src Source, old, cfg config.Config, tgt Target, getenv func(string) string) []Item {
	type want struct {
		code   string
		params map[string]any
		item   devmigrate.Item
	}
	var items []want
	add := func(code string, it devmigrate.Item, kv ...any) {
		items = append(items, want{code: code, params: params(kv), item: it})
	}
	for _, k := range src.Keys {
		name := filepath.Base(k)
		add("key", devmigrate.Item{What: name, From: k, To: filepath.Join(tgt.KeysDir, name), Key: true}, "name", name)
	}
	if src.DB == DBNode && src.NodeConfig != "" {
		add("node_config", devmigrate.Item{What: "node config", From: src.NodeConfig, To: tgt.NodeConfig, Key: true})
	}
	if src.Kind != KindNodeWorker && src.ConfigFound {
		state := old.StateDir()
		add("search_index", devmigrate.Item{What: "search index", From: filepath.Join(state, "chroma"), To: filepath.Join(tgt.StateDir, "chroma")})
		add("streaming_cache", devmigrate.Item{What: "streaming cache", From: filepath.Join(state, "cache", "streaming"),
			To: filepath.Join(tgt.StateDir, "cache", "streaming")})
		add("models", devmigrate.Item{What: "models", From: filepath.Join(state, "models"), To: filepath.Join(tgt.StateDir, "models")})
		if from, ok := sqliteFile(src, old); ok {
			to := sqliteTarget(cfg)
			for _, ext := range []string{"", "-wal", "-shm"} {
				add("database"+strings.ReplaceAll(ext, "-", "_"), devmigrate.Item{What: "library database" + ext, From: from + ext, To: to + ext})
			}
		}
	}
	// after the models (a sign-in among them stays): the second listen's model is gated
	if token := hfToken(getenv); token != "" {
		add("hf_token", devmigrate.Item{What: "Hugging Face sign-in", From: token,
			To: filepath.Join(tgt.StateDir, "models", "huggingface", "token")})
	}
	out := []Item{}
	for _, w := range items {
		it := w.item
		if samePath(it.From, it.To) {
			continue
		}
		it = statItem(it)
		if !it.Exists {
			continue
		}
		out = append(out, Item{Code: w.code, Params: w.params, What: it.What, From: it.From, To: it.To, Size: it.Size, Done: it.Done, item: it})
	}
	return out
}

// hfToken is the Hugging Face sign-in the old install's pipeline used
// (huggingface_hub's own lookup: HF_TOKEN_PATH, HF_HOME, ~/.cache/huggingface).
func hfToken(getenv func(string) string) string {
	if p := strings.TrimSpace(getenv("HF_TOKEN_PATH")); p != "" {
		return p
	}
	if h := strings.TrimSpace(getenv("HF_HOME")); h != "" {
		return filepath.Join(h, "token")
	}
	if home := strings.TrimSpace(getenv("USERPROFILE")); home != "" {
		return filepath.Join(home, ".cache", "huggingface", "token")
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, ".cache", "huggingface", "token")
	}
	return ""
}

// sameBytes: both files read and hold the same bytes (a key, a node config).
func sameBytes(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

// statItem fills in what is there.
func statItem(it devmigrate.Item) devmigrate.Item {
	if _, err := os.Lstat(it.From); err != nil {
		return it
	}
	it.Exists = true
	it.Size = treeSize(it.From)
	if _, err := os.Lstat(it.To); err == nil {
		it.Done = true
	}
	return it
}

func treeSize(p string) int64 {
	var n int64
	_ = filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// sqliteFile is the old install's SQLite database (a relative path is the
// script's folder's).
func sqliteFile(src Source, old config.Config) (string, bool) {
	if src.DB != DBSQLite {
		return "", false
	}
	info, err := dsn.Classify(old.Database.URL)
	if err != nil || info.Backend != dsn.SQLite || info.SQLitePath == "" {
		return "", false
	}
	p := info.SQLitePath
	if !filepath.IsAbs(p) && !isWinAbs(p) {
		p = filepath.Join(src.Dir, p)
	}
	return filepath.Clean(p), true
}

func sqliteTarget(cfg config.Config) string { return filepath.Join(cfg.Server.DataDir, "maktaba.db") }

func existingParent(p string) string {
	for p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return p
}

// Progress hears each step.
type Progress func(stage, message string)

// The old install's stop: its task ended, its restart loop ended at once, its
// processes given stopGrace to exit (an install older than the app has no
// stop event to ask), then ended with their trees (the pipeline is a child of
// its maktaba-server); all of it, and its ports freed, within stopWait. Then
// nothing of it may come back for settleFor(src): longer than its script's
// restart delay.
var (
	stopGrace  = 15 * time.Second
	stopWait   = 90 * time.Second
	stopPoll   = 500 * time.Millisecond // how often it looks again
	stopSettle = 5 * time.Second        // beyond a loop's delay; a stop without a loop waits this
	settleFor  = loopSettle             // tests shorten it
)

// loopSettle is how long the stop watches a quiet old install before it counts
// it stopped: beyond the restart delay of a script that loops (this PC's waits
// 10 s), three times stopSettle for a loop that names no delay.
func loopSettle(src Source) time.Duration {
	switch {
	case src.Loops && src.RestartDelay > 0:
		return src.RestartDelay + stopSettle
	case src.Loops:
		return 3 * stopSettle
	}
	return stopSettle
}

// Move moves the plan's source into the app (move-an-install.md "What a move
// does"): the venv built first (the old install still runs), the old task's
// XML kept, the old install stopped and its task disabled, its keys, node
// config, files and settings carried. The app's server then starts (the shell
// registers and runs its task); Verify and Finish follow. A move that stopped
// part way goes on from its last step when run again.
func Move(ctx context.Context, sys Sys, p Plan, tgt Target, venv func(context.Context) error, progress Progress) (Record, error) {
	if progress == nil {
		progress = func(string, string) {}
	}
	if len(p.Problems) > 0 {
		return Record{}, errors.New(p.ProblemsText() + ": nothing was changed")
	}
	src := p.Source
	rec, ok, err := ReadRecord(tgt.StateDir)
	if err != nil {
		return rec, err
	}
	switch {
	case !ok || rec.Stage == StageUndone:
		rec = Record{Stage: StageFound, StartedAt: time.Now().UTC()}
	case rec.Stage != StageFound && rec.Stage != StageStopped:
		return rec, codedf("adopt_moved_already", []any{"source", rec.Source.ID},
			"%s was moved into the app already (%s)", rec.Source.ID, RecordPath(tgt.StateDir))
	case rec.Source.ID != src.ID:
		return rec, codedf("adopt_under_way", []any{"source", rec.Source.ID},
			"the move of %s is under way: it goes on first", rec.Source.ID)
	}
	rec.Source = src
	if err := writeRecord(tgt.StateDir, rec); err != nil {
		return rec, err
	}

	if venv != nil {
		progress("venv", "Installing the transcription tools (the old install keeps running meanwhile)")
		if err := venv(ctx); err != nil {
			return rec, fmt.Errorf("the transcription tools: %w", err)
		}
	}
	if rec.Stage == StageFound {
		// the definitions first, in the record: a stop that fails part way has
		// turned tasks off, and Undo turns them on again from these
		saved := map[string]bool{}
		for _, s := range rec.Saved {
			saved[s.Task] = true // a run again keeps the first (still enabled) one
		}
		for _, task := range src.Tasks() {
			if saved[task] {
				continue
			}
			raw, err := sys.TaskXML(task)
			if err != nil {
				return rec, codedf("adopt_task", []any{"task", task}, "read the old task %s: %w", task, err)
			}
			s := Saved{Task: task, XML: filepath.Join(tgt.StateDir, "moved", safeName(task)+".xml")}
			if err := layout.WriteFileAtomic(s.XML, raw, 0o600); err != nil {
				return rec, err
			}
			rec.Saved = append(rec.Saved, s)
		}
		if err := writeRecord(tgt.StateDir, rec); err != nil {
			return rec, err
		}
		progress("stop", "Stopping the old install")
		if err := stopSource(ctx, sys, src, tgt.self()); err != nil {
			return rec, err
		}
		rec.Stage = StageStopped
		if err := writeRecord(tgt.StateDir, rec); err != nil {
			return rec, err
		}
	}

	progress("copy", "Copying its keys and files into the app")
	rec.Copied = []string{}
	for _, it := range p.Items {
		if err := it.item.Copy(false); err != nil {
			var differs *devmigrate.DiffersError
			if errors.As(err, &differs) {
				return rec, coded("adopt_key_differs", err, "path", differs.To, "from", differs.From)
			}
			return rec, err
		}
		rec.Copied = append(rec.Copied, it.What)
	}
	progress("settings", "Carrying its settings")
	if err := p.config.SaveTo(tgt.ConfigPath); err != nil {
		return rec, err
	}
	rec.Carried, rec.NotCarried = p.Carried, p.NotCarried
	rec.Stage, rec.MovedAt = StageMoved, now()
	return rec, writeRecord(tgt.StateDir, rec)
}

// stopSource ends the old install: its task ended and disabled (it must not
// start again at the next sign-in); the cmd.exe running its script (a restart
// loop: started by hand, or outliving the task's end) ended at once; its
// processes waited for, and ended with their trees when they outlive
// stopGrace; its ports free; then nothing of it back for settleFor(src), so a
// loop the stop did not see is caught. Nothing that is not Maktaba's is
// touched, nor the app's own install or this process (me). A process list it
// cannot read stops the move: it cannot tell what still runs.
func stopSource(ctx context.Context, sys Sys, src Source, me self) error {
	for _, task := range src.Tasks() {
		_ = sys.EndTask(task) // a task that is not running is not an error; what outlives it is ended below
		if err := sys.SetTaskEnabled(task, false); err != nil {
			return codedf("adopt_task", []any{"task", task}, "disable the old task %s: %w", task, err)
		}
	}
	start := time.Now()
	grace, deadline := start.Add(stopGrace), start.Add(stopWait)
	if src.Kind == KindProcess {
		grace = start // no task to end: nothing else asks it to stop
	}
	settle := settleFor(src)
	killed, ended := map[int]bool{}, map[int]bool{}
	var quiet time.Time // since when nothing of it runs and its ports are free
	for {
		procs, err := sys.Processes()
		if err != nil {
			return codedf("adopt_processes_unknown", nil,
				"cannot list the running programs, so the old install may still run: %w", err)
		}
		var left []int
		for _, p := range procs {
			switch {
			case me.is(p):
			case src.runsScript(p):
				// its restart loop: ended alone, at once (the server it started
				// keeps its grace)
				if !ended[p.PID] {
					ended[p.PID] = true
					_ = sys.EndProcess(p.PID)
				}
				left = append(left, p.PID)
			case src.owns(p) || (!isConsole(p) && slices.Contains(src.PIDs, p.PID)):
				left = append(left, p.PID)
				if time.Now().After(grace) && !killed[p.PID] {
					killed[p.PID] = true
					_ = sys.Kill(p.PID)
				}
			}
		}
		busy := ""
		if len(left) == 0 {
			busy = busyPort(sys, src)
		}
		now := time.Now()
		switch {
		case len(left) > 0 || busy != "":
			quiet = time.Time{}
			if now.After(deadline) {
				if len(left) > 0 {
					pids := make([]string, len(left))
					for i, pid := range left {
						pids[i] = strconv.Itoa(pid)
					}
					return codedf("adopt_still_running", []any{"pids", strings.Join(pids, ", ")},
						"the old install's processes (%v) are still running", left)
				}
				return codedf("adopt_port_busy", []any{"port", busy}, "port %s is still in use after the old install stopped", busy)
			}
		case quiet.IsZero():
			quiet = now
		case now.Sub(quiet) >= settle:
			return nil
		}
		if !sleepCtx(ctx, stopPoll) {
			return ctx.Err()
		}
	}
}

// busyPort is the first of the source's ports something still listens on.
func busyPort(sys Sys, src Source) string {
	names := make([]string, 0, len(src.Ports))
	for name := range src.Ports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		addr := src.Ports[name]
		if port := addr[strings.LastIndexByte(addr, ':')+1:]; port != "" && sys.Listening(port) {
			return port
		}
	}
	return ""
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// What a script's set lines mean for the app.
var (
	// they only located the old install's programs, or the app sets them itself
	droppedEnv = map[string]bool{
		"MAKTABA_CONFIG": true, "MAKTABA_API_BIN": true, "MAKTABA_STREAMING_BIN": true, "MAKTABA_PYTHON": true,
		"MAKTABA_MIGRATIONS_DIR": true, "MAKTABA_NODE_CONFIG": true, "PYTHONIOENCODING": true,
		"PYTHONUNBUFFERED": true, "PYTHONUTF8": true,
	}
	// they are server.toml settings
	carriedEnv = map[string]func(*config.Config, string){
		"MAKTABA_PATH_MAP":        func(c *config.Config, v string) { setIfEmpty(&c.Media.PathMap, v) },
		"MAKTABA_WORKER_PATH_MAP": func(c *config.Config, v string) { setIfEmpty(&c.Media.PathMap, v) },
		"MAKTABA_MEDIA_ROOTS": func(c *config.Config, v string) {
			if len(c.Media.Roots) == 0 {
				for _, r := range strings.Split(v, ",") {
					if r = strings.TrimSpace(r); r != "" {
						c.Media.Roots = append(c.Media.Roots, r)
					}
				}
			}
		},
		"MAKTABA_STT_MODEL":        func(c *config.Config, v string) { c.Transcription.Model = strings.TrimSpace(v) },
		"MAKTABA_STT_REFINE_MODEL": func(c *config.Config, v string) { c.Transcription.RefineModel = strings.TrimSpace(v) },
		"MAKTABA_STT_REFINE": func(c *config.Config, v string) {
			c.Transcription.Refine = "on"
			if off(v) {
				c.Transcription.Refine = "off"
			}
		},
		"MAKTABA_STT_AUDIO_HOURS":  func(c *config.Config, v string) { setFloat(&c.Transcription.AudioHours, v) },
		"MAKTABA_STT_GPU_GB":       func(c *config.Config, v string) { setFloat(&c.Transcription.GPUGB, v) },
		"MAKTABA_STT_GPU_CACHE_GB": func(c *config.Config, v string) { setFloat(&c.Transcription.GPUCacheGB, v) },
		"MAKTABA_STT_MIN_FREE_PCT": func(c *config.Config, v string) { setFloat(&c.Transcription.MinFreePct, v) },
		"MAKTABA_PUBLIC_BASE_URL":  func(c *config.Config, v string) { setIfEmpty(&c.Server.PublicURL, v) },
		"MAKTABA_DISCOVERY_MDNS": func(c *config.Config, v string) {
			if off(v) {
				c.Server.MDNS = "off"
			}
		},
	}
)

func off(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

func setIfEmpty(dst *string, v string) {
	if strings.TrimSpace(*dst) == "" {
		*dst = strings.TrimSpace(v)
	}
}

func setFloat(dst *float64, v string) {
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
		*dst = f
	}
}

// carrySettings is the app's server.toml with the old install's settings: its
// addresses (the ports other devices and the home know), media, transcription,
// TV, pipeline, cloud and supervisor settings; never its data_dir (the app's
// own); its database url only for a Postgres (the SQLite file is copied to the
// app's data folder); and what its script set that is a setting. It returns
// what was carried and what could not be.
func carrySettings(src Source, old config.Config, tgt Target) (config.Config, []Note, []Note, error) {
	cfg, found, err := config.LoadFrom(tgt.ConfigPath)
	if err != nil {
		return cfg, nil, nil, err
	}
	if !found {
		cfg = config.Defaults()
	}
	carried, notCarried := []Note{}, []Note{}
	if src.Kind == KindNodeWorker {
		// the app's server replaces the workers: a full node with as many pipeline
		// workers as the installer ran (Epic 35 story 35.8)
		cfg.Pipeline.Instances, cfg.PipelineInstancesSet = len(src.Tasks()), true
		carried = append(carried, note("pipeline_instances", fmt.Sprintf("[pipeline] instances = %d", len(src.Tasks())),
			"count", len(src.Tasks())))
		return cfg, carried, notCarried, nil
	}
	if src.ConfigFound {
		dataDir, appPython := cfg.Server.DataDir, cfg.Pipeline.Python
		cfg.Server = old.Server
		cfg.Server.DataDir = dataDir
		cfg.Media, cfg.Transcription, cfg.TV, cfg.Cloud, cfg.Supervisor = old.Media, old.Transcription, old.TV, old.Cloud, old.Supervisor
		cfg.Pipeline, cfg.PipelineInstancesSet = old.Pipeline, old.PipelineInstancesSet
		cfg.Pipeline.Python = appPython
		if strings.TrimSpace(old.Pipeline.Python) != "" {
			notCarried = append(notCarried, note("pipeline_python", "[pipeline] python (the app runs its own)"))
		}
		for _, section := range []string{"[server]", "[media]", "[transcription]", "[tv]", "[pipeline]", "[cloud]", "[supervisor]"} {
			carried = append(carried, note("section", section, "name", section))
		}
		switch src.DB {
		case DBPostgres, DBOwnPostgres:
			cfg.Database, cfg.DatabaseURLSet = old.Database, true
			carried = append(carried, note("database_url", "[database] url"))
		case DBSQLite:
			cfg.Database.URL, cfg.DatabaseURLSet = "sqlite://"+filepath.ToSlash(sqliteTarget(cfg)), true
		case DBNode:
			// a node reads the home's database (node.json): its own url is not used
		}
	}
	for _, name := range src.EnvOrder {
		v := src.Env[name]
		switch {
		case droppedEnv[name]:
		case carriedEnv[name] != nil:
			carriedEnv[name](&cfg, v)
			carried = append(carried, note("env", name, "name", name))
		case strings.HasPrefix(name, "MAKTABA_"):
			notCarried = append(notCarried, note("env", name+" (no server.toml setting)", "name", name))
		}
	}
	for i := 0; i < len(src.ServeArgs); i++ {
		switch a := src.ServeArgs[i]; {
		case a == "--no-mdns":
			cfg.Server.MDNS = "off"
			carried = append(carried, note("no_mdns", `--no-mdns as [server] mdns = "off"`))
		case a == "--bin-dir" || a == "--grace":
			i++ // the app's own programs; its own stop grace
		case strings.HasPrefix(a, "--bin-dir=") || strings.HasPrefix(a, "--grace="):
		default:
			if i+1 < len(src.ServeArgs) && !strings.HasPrefix(src.ServeArgs[i+1], "-") {
				i++
				a += " " + src.ServeArgs[i]
			}
			notCarried = append(notCarried, note("serve_flag", "serve "+a, "flag", a))
		}
	}
	return cfg, carried, notCarried, nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeName(s string) string { return strings.Trim(unsafeChars.ReplaceAllString(s, "_"), "_") }

func human(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// Finish removes the old task once the app's server was verified (its XML
// stays in moved/ for Undo). ok false: no verified move waits.
func Finish(sys Sys, stateDir string) (bool, error) {
	rec, ok, err := ReadRecord(stateDir)
	if err != nil || !ok || rec.Stage != StageVerified {
		return false, err
	}
	for _, task := range rec.Source.Tasks() {
		if err := sys.DeleteTask(task); err != nil {
			return false, fmt.Errorf("remove the old task %s: %w", task, err)
		}
	}
	rec.Stage, rec.FinishedAt = StageFinished, now()
	return true, writeRecord(stateDir, rec)
}

// Undo runs the old install again: the app's server paused, stopped and this
// user's app tasks disabled (theirs, and their task of the old shared name);
// the app window out of server mode; the old task registered again from its
// XML (or enabled again, when it was not removed yet) and run. What changed in
// the app since stays in the app.
//
// The app's server never starts over the old install by itself afterwards: in
// this sign-in the pause flag holds and the tasks are off; at the next sign-in
// the tasks are still off (the pause flag ends with the sign-in), and the app
// window opens setup, whose status step does not take the server back while
// the record says undone (UndoneHere): the welcome screen offers the move
// again.
//
// A move that stopped part way is undone too (the setup screen's "Restore the
// old install"): its tasks, turned off by a stop that failed, are turned on
// again from the record. An old task is run only while nothing of the old
// install runs (a stop that failed may have left it running).
func Undo(sys Sys, tgt Target) error {
	rec, ok, err := ReadRecord(tgt.StateDir)
	if err != nil {
		return err
	}
	if !ok || rec.Stage == StageUndone {
		return coded("adopt_no_move", errors.New("no move to undo on this PC"))
	}
	apps, err := appTasks(sys)
	if err != nil {
		return err
	}
	if err := layout.WriteFileAtomic(tgt.pausedFlag(), []byte("paused: the install moved into the app runs again\n"), 0o600); err != nil {
		return err
	}
	if err := sys.StopAppServer(); err != nil {
		return err
	}
	for _, task := range apps {
		if err := sys.SetTaskEnabled(task, false); err != nil && sys.HasTask(task) {
			return fmt.Errorf("disable the app's task %s: %w", task, err)
		}
	}
	if err := leaveServerMode(tgt.ModeFile); err != nil {
		return err
	}
	// with the app's server stopped: what still runs is the old install's
	runs, err := sourceRuns(sys, rec.Source, tgt.self())
	if err != nil {
		return err
	}
	for _, s := range rec.Saved {
		if !sys.HasTask(s.Task) {
			if err := sys.CreateTask(s.Task, s.XML); err != nil {
				return fmt.Errorf("register the old task %s again: %w", s.Task, err)
			}
		}
		if err := sys.SetTaskEnabled(s.Task, true); err != nil {
			return fmt.Errorf("enable the old task %s again: %w", s.Task, err)
		}
		if runs {
			continue // it never stopped: a second copy would fight it for its ports
		}
		if err := sys.RunTask(s.Task); err != nil {
			return fmt.Errorf("start the old task %s: %w", s.Task, err)
		}
	}
	rec.Stage, rec.UndoneAt = StageUndone, now()
	return writeRecord(tgt.StateDir, rec)
}

// sourceRuns reports whether a program of src runs (never the app's own, me).
// A process list it cannot read is an error: not sure is not "no".
func sourceRuns(sys Sys, src Source, me self) (bool, error) {
	procs, err := sys.Processes()
	if err != nil {
		return false, codedf("adopt_processes_unknown", nil, "cannot list the running programs: %w", err)
	}
	for _, p := range procs {
		if !me.is(p) && (src.owns(p) || src.runsScript(p)) {
			return true, nil
		}
	}
	return false, nil
}

// leaveServerMode takes the app window out of server mode: a mode.json that
// says server is removed, so the window opens setup instead of waiting on (and
// offering to turn on) a server that gave this PC back to the old install. A
// client mode, or none, stays as it is.
func leaveServerMode(path string) error {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Mode != "server" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("take the app out of server mode (%s): %w", path, err)
	}
	return nil
}

// UndoneHere reports whether the move recorded here was undone: the old
// install runs this PC's Maktaba again, and the app must not take it back by
// itself (setup's status step says the app is not set up).
func UndoneHere(stateDir string) bool {
	rec, ok, err := ReadRecord(stateDir)
	return err == nil && ok && rec.Stage == StageUndone
}

// ForgetUndone removes the record of a move that was undone, once the app is
// set up afresh (setup's finish): the app is this PC's server again.
func ForgetUndone(stateDir string) error {
	if !UndoneHere(stateDir) {
		return nil
	}
	if err := os.Remove(RecordPath(stateDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
