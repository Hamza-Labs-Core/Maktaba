package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/devmigrate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// AppTask is the app's own sign-in task (contract-agent).
const AppTask = `\Maktaba Server`

// Target is the app the install moves into.
type Target struct {
	ConfigPath string // the app's server.toml
	StateDir   string // the app's state root (moved/, chroma/, models/, cache/)
	KeysDir    string
	NodeConfig string // where the app reads node.json
}

// pausedFlag is the shell's pause flag (agent.rs): a paused server is not
// started by the app until the owner resumes it.
func (t Target) pausedFlag() string { return filepath.Join(t.StateDir, "state", "server-paused") }

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
	Carried    []string   `json:"carried"`
	NotCarried []string   `json:"not_carried"`
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

// Item is one file or folder the move copies into the app.
type Item struct {
	What string `json:"what"`
	From string `json:"from"`
	To   string `json:"to"`
	Size int64  `json:"size"`
	Done bool   `json:"done"` // already in the app (left as it is)
	item devmigrate.Item
}

// Plan is what a move of one source would do, read without changing anything.
type Plan struct {
	Source     Source   `json:"source"`
	Items      []Item   `json:"items"`
	Carried    []string `json:"carried"`
	NotCarried []string `json:"not_carried"`
	// Problems stop the move (nothing is changed while any is left).
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
	Need     int64    `json:"need"` // bytes the copies take
	Free     int64    `json:"free"` // free bytes where the app keeps its state (-1 unknown)

	config config.Config // the app's server.toml after the move
}

// MakePlan reads what moving src into tgt does.
func MakePlan(sys Sys, src Source, tgt Target) (Plan, error) {
	p := Plan{Source: src, Items: []Item{}, Problems: []string{}, Warnings: []string{}, Free: -1}
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
		if !it.Done {
			p.Need += it.Size
		}
	}
	if free := hostinfo.FreeDisk(existingParent(tgt.StateDir)); free > 0 {
		p.Free = int64(free)
		if p.Need > p.Free {
			p.Problems = append(p.Problems, fmt.Sprintf("the copies need %s and %s is free where the app keeps its files",
				human(p.Need), human(p.Free)))
		}
	}
	switch src.DB {
	case DBNode:
		if src.Kind == KindNodeWorker && src.Home == "" {
			p.Problems = append(p.Problems, "this worker has not joined a home (no node config at "+src.NodeConfig+")")
		}
	case DBOwnPostgres:
		if _, port, _ := net.SplitHostPort(src.DBWhere); port != "" && !sys.Listening(port) {
			p.Problems = append(p.Problems, "its library database, the Postgres at "+src.DBWhere+
				", is not running: the move needs it running")
		}
		p.Warnings = append(p.Warnings, "the library database stays in the Postgres at "+src.DBWhere+
			" on this PC, which keeps running as it does now")
	}
	if src.Kind == KindProcess {
		p.Warnings = append(p.Warnings, "it was started without a task: the move ends it")
	}
	return p, nil
}

// moveItems are the files the app takes over: the keys and the node config
// (never replaced when the app has a different one), the search index, the
// streaming cache, the models, the Hugging Face sign-in and an SQLite database.
func moveItems(src Source, old, cfg config.Config, tgt Target, getenv func(string) string) []Item {
	var items []devmigrate.Item
	for _, k := range src.Keys {
		items = append(items, devmigrate.Item{What: filepath.Base(k), From: k, To: filepath.Join(tgt.KeysDir, filepath.Base(k)), Key: true})
	}
	if src.DB == DBNode && src.NodeConfig != "" {
		items = append(items, devmigrate.Item{What: "node config", From: src.NodeConfig, To: tgt.NodeConfig, Key: true})
	}
	if src.Kind != KindNodeWorker && src.ConfigFound {
		state := old.StateDir()
		items = append(items,
			devmigrate.Item{What: "search index", From: filepath.Join(state, "chroma"), To: filepath.Join(tgt.StateDir, "chroma")},
			devmigrate.Item{What: "streaming cache", From: filepath.Join(state, "cache", "streaming"), To: filepath.Join(tgt.StateDir, "cache", "streaming")},
			devmigrate.Item{What: "models", From: filepath.Join(state, "models"), To: filepath.Join(tgt.StateDir, "models")},
		)
		if from, ok := sqliteFile(src, old); ok {
			to := sqliteTarget(cfg)
			for _, ext := range []string{"", "-wal", "-shm"} {
				items = append(items, devmigrate.Item{What: "library database" + ext, From: from + ext, To: to + ext})
			}
		}
	}
	// after the models (a sign-in among them stays): the second listen's model is gated
	if token := hfToken(getenv); token != "" {
		items = append(items, devmigrate.Item{What: "Hugging Face sign-in", From: token,
			To: filepath.Join(tgt.StateDir, "models", "huggingface", "token")})
	}
	out := []Item{}
	for _, it := range items {
		if samePath(it.From, it.To) {
			continue
		}
		it = statItem(it)
		if !it.Exists {
			continue
		}
		out = append(out, Item{What: it.What, From: it.From, To: it.To, Size: it.Size, Done: it.Done, item: it})
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

// The old install's stop: its task ended, its processes given stopGrace to
// exit (an install older than the app has no stop event to ask), then ended
// with their trees (the pipeline is a child of its maktaba-server); all of it,
// and its ports freed, within stopWait.
var (
	stopGrace = 15 * time.Second
	stopWait  = 90 * time.Second
)

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
		return Record{}, errors.New(strings.Join(p.Problems, "; ") + ": nothing was changed")
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
		return rec, fmt.Errorf("%s was moved into the app already (%s)", rec.Source.ID, RecordPath(tgt.StateDir))
	case rec.Source.ID != src.ID:
		return rec, fmt.Errorf("the move of %s is under way: it goes on first", rec.Source.ID)
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
		rec.Saved = []Saved{}
		for _, task := range src.Tasks() {
			raw, err := sys.TaskXML(task)
			if err != nil {
				return rec, fmt.Errorf("read the old task %s: %w", task, err)
			}
			s := Saved{Task: task, XML: filepath.Join(tgt.StateDir, "moved", safeName(task)+".xml")}
			if err := layout.WriteFileAtomic(s.XML, raw, 0o600); err != nil {
				return rec, err
			}
			rec.Saved = append(rec.Saved, s)
		}
		progress("stop", "Stopping the old install")
		if err := stopSource(ctx, sys, src); err != nil {
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
// start again at the next sign-in), then its processes waited for, and ended
// with their trees when they outlive stopGrace; its ports must be free before
// the app's server takes them. Nothing that is not Maktaba's is touched.
func stopSource(ctx context.Context, sys Sys, src Source) error {
	for _, task := range src.Tasks() {
		_ = sys.EndTask(task) // a task that is not running is not an error
		if err := sys.SetTaskEnabled(task, false); err != nil {
			return fmt.Errorf("disable the old task %s: %w", task, err)
		}
	}
	alive := func() []int {
		procs, err := sys.Processes()
		if err != nil {
			return nil
		}
		var pids []int
		for _, p := range procs {
			mine := src.owns(p)
			for _, pid := range src.PIDs {
				mine = mine || p.PID == pid
			}
			if mine {
				pids = append(pids, p.PID)
			}
		}
		return pids
	}
	start := time.Now()
	grace, deadline := start.Add(stopGrace), start.Add(stopWait)
	if src.Kind == KindProcess {
		grace = start // no task to end: nothing else asks it to stop
	}
	killed := map[int]bool{}
	for left := alive(); len(left) > 0; left = alive() {
		switch {
		case time.Now().After(deadline):
			return fmt.Errorf("the old install's processes (%v) are still running", left)
		case time.Now().After(grace):
			for _, pid := range left {
				if !killed[pid] {
					killed[pid] = true
					_ = sys.Kill(pid)
				}
			}
		}
		if !sleepCtx(ctx, 500*time.Millisecond) {
			return ctx.Err()
		}
	}
	for _, addr := range src.Ports {
		port := addr[strings.LastIndexByte(addr, ':')+1:]
		for port != "" && sys.Listening(port) {
			if time.Now().After(deadline) {
				return fmt.Errorf("port %s is still in use after the old install stopped", port)
			}
			if !sleepCtx(ctx, 500*time.Millisecond) {
				return ctx.Err()
			}
		}
	}
	return nil
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
func carrySettings(src Source, old config.Config, tgt Target) (config.Config, []string, []string, error) {
	cfg, found, err := config.LoadFrom(tgt.ConfigPath)
	if err != nil {
		return cfg, nil, nil, err
	}
	if !found {
		cfg = config.Defaults()
	}
	carried, notCarried := []string{}, []string{}
	if src.Kind == KindNodeWorker {
		// the app's server replaces the workers: a full node with as many pipeline
		// workers as the installer ran (Epic 35 story 35.8)
		cfg.Pipeline.Instances, cfg.PipelineInstancesSet = len(src.Tasks()), true
		carried = append(carried, fmt.Sprintf("[pipeline] instances = %d", len(src.Tasks())))
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
			notCarried = append(notCarried, "[pipeline] python (the app runs its own)")
		}
		carried = append(carried, "[server]", "[media]", "[transcription]", "[tv]", "[pipeline]", "[cloud]", "[supervisor]")
		switch src.DB {
		case DBPostgres, DBOwnPostgres:
			cfg.Database, cfg.DatabaseURLSet = old.Database, true
			carried = append(carried, "[database] url")
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
			carried = append(carried, name)
		case strings.HasPrefix(name, "MAKTABA_"):
			notCarried = append(notCarried, name+" (no server.toml setting)")
		}
	}
	for i := 0; i < len(src.ServeArgs); i++ {
		switch a := src.ServeArgs[i]; {
		case a == "--no-mdns":
			cfg.Server.MDNS = "off"
			carried = append(carried, `--no-mdns as [server] mdns = "off"`)
		case a == "--bin-dir" || a == "--grace":
			i++ // the app's own programs; its own stop grace
		case strings.HasPrefix(a, "--bin-dir=") || strings.HasPrefix(a, "--grace="):
		default:
			if i+1 < len(src.ServeArgs) && !strings.HasPrefix(src.ServeArgs[i+1], "-") {
				i++
				a += " " + src.ServeArgs[i]
			}
			notCarried = append(notCarried, "serve "+a)
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

// Undo runs the old install again: the app's server paused, stopped and its
// task disabled; the old task registered again from its XML (or enabled again,
// when it was not removed yet) and run. What changed in the app since stays in
// the app.
func Undo(sys Sys, tgt Target) error {
	rec, ok, err := ReadRecord(tgt.StateDir)
	if err != nil {
		return err
	}
	if !ok || rec.Stage == StageUndone || rec.Stage == StageFound {
		return errors.New("no move to undo on this PC")
	}
	if err := layout.WriteFileAtomic(tgt.pausedFlag(), []byte("paused: the install moved into the app runs again\n"), 0o600); err != nil {
		return err
	}
	if err := sys.StopAppServer(); err != nil {
		return err
	}
	if err := sys.SetTaskEnabled(AppTask, false); err != nil && sys.HasTask(AppTask) {
		return fmt.Errorf("disable the app's task: %w", err)
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
		if err := sys.RunTask(s.Task); err != nil {
			return fmt.Errorf("start the old task %s: %w", s.Task, err)
		}
	}
	rec.Stage, rec.UndoneAt = StageUndone, now()
	return writeRecord(tgt.StateDir, rec)
}
