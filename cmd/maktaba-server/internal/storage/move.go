package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Files beside server.toml (Install.ConfigDir).
const (
	// JournalFile is a move under way: where from and to, and how far it came. A
	// move cut short (a crash, a power cut, the disk pulled) resumes from it.
	JournalFile = "storage-move.json"
	// ResultFile is how the last move ended, for storage show and the Storage page.
	ResultFile = "storage-moved.json"
	// RequestFile is a move asked for from the web: the server's next start does it
	// before anything else runs.
	RequestFile = "storage-move-request.json"
)

// movedInfix names an old copy a move kept: <folder>.moved-<stamp>.
const movedInfix = ".moved-"

// lockWait is how long a move waits for another holder of the move lock.
var lockWait = 30 * time.Second

// Layout is where an install's data folder ([server] data_dir) and its state
// root (Config.StateDir) are: the same folder for any data_dir the owner chose,
// the root beside data/ for the default one on macOS and Windows.
type Layout struct {
	Data string `json:"data"`
	Root string `json:"root"`
}

// Note is one finding of a plan: a code the screens translate, its values and the
// English.
type Note struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}

func note(code, msg string, kv ...any) Note {
	n := Note{Code: code, Message: msg}
	for i := 0; i+1 < len(kv); i += 2 {
		if n.Params == nil {
			n.Params = map[string]any{}
		}
		n.Params[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return n
}

// Entry is one folder a move copies.
type Entry struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
	// Keep: copied and merged into what is there (the newer file wins), the original
	// left where it is: state/, where the desktop app keeps files of its own.
	Keep bool `json:"keep,omitempty"`
	// Exclude are names at the top of From that are not this entry's (the state
	// root's folders inside a data folder, the old marker).
	Exclude []string `json:"exclude,omitempty"`
	Bytes   uint64   `json:"bytes"`
	Files   int      `json:"files"`
}

// Plan is what a move to Target would do, and why it cannot.
type Plan struct {
	Target    string  `json:"target"`
	From      Layout  `json:"from"`
	To        Layout  `json:"to"`
	Entries   []Entry `json:"entries"`
	NeedBytes uint64  `json:"need_bytes"`
	FreeBytes uint64  `json:"free_bytes"`
	Disk      Disk    `json:"disk"`
	// Resume: a move to Target was cut short; this one finishes it.
	Resume   bool   `json:"resume"`
	OK       bool   `json:"ok"`
	Problems []Note `json:"problems"`
	Warnings []Note `json:"warnings"`
}

func (p *Plan) problem(n Note) { p.Problems = append(p.Problems, n); p.OK = false }

// PlanError is a move refused by its plan.
type PlanError struct{ Plan Plan }

func (e *PlanError) Error() string {
	var msgs []string
	for _, n := range e.Plan.Problems {
		msgs = append(msgs, n.Message)
	}
	return "cannot move Maktaba's data to " + e.Plan.Target + ": " + strings.Join(msgs, "; ")
}

// Install is the install a move works on.
type Install struct {
	// ConfigDir holds server.toml, the move lock, the journal and the record.
	ConfigDir string
	// ConfigPath is the server.toml whose data_dir the move sets.
	ConfigPath string
	// From is where the install's data is now.
	From Layout
	// ToLayout is the layout data_dir = target gives (Config.StateDirFor).
	ToLayout func(target string) Layout
}

// InstallOf is cfg's install, as it reads the server.toml at configPath.
func InstallOf(cfg config.Config, configPath string) Install {
	return Install{
		ConfigDir:  filepath.Dir(configPath),
		ConfigPath: configPath,
		From:       Layout{Data: filepath.Clean(cfg.Server.DataDir), Root: filepath.Clean(cfg.StateDir())},
		ToLayout: func(target string) Layout {
			return Layout{Data: filepath.Clean(target), Root: filepath.Clean(cfg.StateDirFor(target))}
		},
	}
}

// journal is JournalFile.
type journal struct {
	Target  string    `json:"target"`
	From    Layout    `json:"from"`
	To      Layout    `json:"to"`
	Stamp   string    `json:"stamp"`
	Phase   string    `json:"phase"` // copying | verified | switched
	Entries []Entry   `json:"entries"`
	Started time.Time `json:"started"`
}

// Journal phases.
const (
	phaseCopying  = "copying"
	phaseVerified = "verified"
	phaseSwitched = "switched"
)

// Result is ResultFile: how the last move ended.
type Result struct {
	Target string    `json:"target"`
	From   Layout    `json:"from"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
	// Old are the old copies kept aside (<folder>.moved-<stamp>), to remove by hand.
	Old []string `json:"old,omitempty"`
	// Bytes and Files are what was copied.
	Bytes uint64 `json:"bytes"`
	Files int    `json:"files"`
}

// Request is RequestFile.
type Request struct {
	Target string    `json:"target"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path) //nolint:gosec // the install's own file
	return err == nil && json.Unmarshal(b, v) == nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(path, append(b, '\n'), 0o600)
}

// ReadJournal is the move under way in configDir, if any.
func ReadJournal(configDir string) (target string, phase string, ok bool) {
	var j journal
	if !readJSON(filepath.Join(configDir, JournalFile), &j) || j.Target == "" {
		return "", "", false
	}
	return j.Target, j.Phase, true
}

// ReadResult is how the last move in configDir ended, if one did.
func ReadResult(configDir string) (Result, bool) {
	var r Result
	ok := readJSON(filepath.Join(configDir, ResultFile), &r) && r.Target != ""
	return r, ok
}

// ReadRequest is the move asked for from the web, if any.
func ReadRequest(configDir string) (Request, bool) {
	var r Request
	ok := readJSON(filepath.Join(configDir, RequestFile), &r) && r.Target != ""
	return r, ok
}

// WriteRequest asks the server's next start to move the data to target.
func WriteRequest(configDir string, r Request) error {
	return writeJSON(filepath.Join(configDir, RequestFile), r)
}

// DropRequest forgets the request (done, or refused).
func DropRequest(configDir string) error {
	err := os.Remove(filepath.Join(configDir, RequestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// WriteFailure records a move that ended without moving (the Storage page says why).
func WriteFailure(configDir, target string, from Layout, err error) {
	_ = writeJSON(filepath.Join(configDir, ResultFile), Result{Target: target, From: from, Error: err.Error(),
		At: time.Now().UTC()})
}

// rootNames are the state root's folders (config.StateRootFolders).
func rootNames() map[string]bool {
	m := map[string]bool{}
	for _, n := range config.StateRootFolders {
		m[n] = true
	}
	return m
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// entries are the folders a move from from to to copies: every state root
// folder there is, and the data folder (less the root's folders when they are in
// it).
func entries(from, to Layout) ([]Entry, []Note) {
	names := rootNames()
	var out []Entry
	var clash []Note
	same := from.Data == from.Root
	for _, n := range config.StateRootFolders {
		src := filepath.Join(from.Root, n)
		if info, err := os.Lstat(src); err != nil || !info.IsDir() {
			continue
		}
		e := Entry{Name: n, From: src, To: filepath.Join(to.Root, n)}
		// the desktop app keeps files of its own in state/ beside its own default
		// root: they stay, a copy goes with the server's
		e.Keep = n == "state" && !same
		out = append(out, e)
	}
	if info, err := os.Stat(from.Data); err == nil && info.IsDir() {
		e := Entry{Name: "data", From: from.Data, To: to.Data, Exclude: []string{config.StorageMarkerFile}}
		if same {
			e.Exclude = append(e.Exclude, config.StateRootFolders...)
		} else if to.Data == to.Root {
			// data/ goes into the folder the root's folders go into: a name in both
			// would be two folders in one place
			kids, _ := os.ReadDir(from.Data)
			for _, k := range kids {
				if names[k.Name()] && exists(filepath.Join(from.Root, k.Name())) {
					clash = append(clash, note("name_clash",
						fmt.Sprintf("%s and %s would both go to %s", filepath.Join(from.Data, k.Name()),
							filepath.Join(from.Root, k.Name()), filepath.Join(to.Data, k.Name())),
						"path", filepath.Join(from.Data, k.Name())))
				}
			}
		}
		out = append(out, e)
	}
	return out, clash
}

// emptyOrAbsent: p is not there, or a folder with nothing in it but the names allowed.
func emptyOrAbsent(p string, allowed ...string) bool {
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil || !info.IsDir() {
		return false
	}
	kids, err := os.ReadDir(p)
	if err != nil {
		return false
	}
	for _, k := range kids {
		ok := false
		for _, a := range allowed {
			ok = ok || k.Name() == a
		}
		if !ok {
			return false
		}
	}
	return true
}

// nearestDir is p or its nearest folder that exists.
func nearestDir(p string) string {
	for {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
		up := filepath.Dir(p)
		if up == p {
			return p
		}
		p = up
	}
}

// writable tries a file in dir's nearest existing folder.
func writable(dir string) error {
	f, err := os.CreateTemp(nearestDir(dir), ".maktaba-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// marginOf is the room a move keeps free on the target beyond what it copies:
// 5% of it, at least 1 GiB (the database's WAL, a backup taken right after).
func marginOf(n uint64) uint64 {
	if m := n / 20; m > 1<<30 {
		return m
	}
	return 1 << 30
}

// tightBelow is the free room after a move under which the plan warns: the
// database and its nightly backups grow with the library.
const tightBelow = 20 << 30

// PlanMove is what moving in's data to target would do, and why it cannot. A move
// to the same target cut short resumes (Plan.Resume); one to another target
// refuses until it is finished.
func PlanMove(in Install, target string) Plan {
	p := Plan{Target: strings.TrimSpace(target), OK: true, Problems: []Note{}, Warnings: []Note{}, From: in.From}
	if p.Target == "" || !filepath.IsAbs(p.Target) {
		p.problem(note("target_relative", "the folder must be a whole path (one that starts at the top of a disk)"))
		return p
	}
	p.Target = filepath.Clean(p.Target)
	var j journal
	hasJournal := readJSON(filepath.Join(in.ConfigDir, JournalFile), &j) && j.Target != ""
	if hasJournal && filepath.Clean(j.Target) != p.Target {
		if j.Phase != phaseCopying {
			// server.toml may point at it already: only that move can finish
			p.problem(note("move_unfinished", "a move to "+j.Target+" is not finished: finish it first (move to it again)",
				"target", j.Target))
			return p
		}
		// a copy cut short that nothing uses yet: this move replaces it, and the
		// partial copy is left for the owner to delete
		p.Warnings = append(p.Warnings, note("partial_copy_left", "the copy a move to "+j.Target+
			" left half made stays there: delete it yourself", "path", j.Target))
		hasJournal = false
	}
	if hasJournal {
		p.Resume, p.From, p.To, p.Entries = true, j.From, j.To, j.Entries
	} else {
		p.To = in.ToLayout(p.Target)
		switch {
		case p.Target == p.From.Data:
			p.problem(note("target_same", "Maktaba's data is in "+p.Target+" already"))
			return p
		case config.Within(p.Target, p.From.Data) || config.Within(p.Target, p.From.Root):
			p.problem(note("target_inside_current", p.Target+" is inside the folder Maktaba keeps its data in now"))
			return p
		case config.Within(p.From.Data, p.Target) || config.Within(p.From.Root, p.To.Root):
			p.problem(note("current_inside_target", "the folder Maktaba keeps its data in now is inside "+p.Target))
			return p
		}
		var clash []Note
		p.Entries, clash = entries(p.From, p.To)
		for _, c := range clash {
			p.problem(c)
		}
		if !emptyOrAbsent(p.To.Data, config.StorageMarkerFile) {
			p.problem(note("target_not_empty", p.To.Data+" is not empty: choose an empty folder or a new one",
				"path", p.To.Data))
		}
		for _, e := range p.Entries {
			if !e.Keep && e.To != p.To.Data && !config.Within(e.To, p.To.Data) && !emptyOrAbsent(e.To) {
				p.problem(note("target_not_empty", e.To+" is not empty", "path", e.To))
			}
		}
	}
	var total, there uint64
	for i := range p.Entries {
		e := &p.Entries[i]
		e.Files, e.Bytes = sizeOf(e.From, e.Exclude)
		total += e.Bytes
		if p.Resume {
			_, b := sizeOf(e.To, e.Exclude)
			there += min(b, e.Bytes)
		}
	}
	p.NeedBytes = total - there + marginOf(total)
	d, err := DiskOf(p.Target)
	if err != nil {
		p.problem(note("target_unwritable", "cannot read the disk of "+p.Target+": "+err.Error(), "error", err.Error()))
		return p
	}
	p.Disk, p.FreeBytes = d, d.FreeBytes
	if !d.Suitable {
		p.problem(note("target_not_local", fmt.Sprintf("%s is on a %s disk (%s), which cannot hold the library database: "+
			"choose a disk of this computer formatted as APFS, Mac OS Extended, NTFS or a Linux file system",
			p.Target, orWord(d.FSType, "network"), d.Mount), "fs_type", d.FSType))
	}
	if d.FreeBytes < p.NeedBytes {
		p.problem(note("target_too_small", fmt.Sprintf("%s has %s free, and the move needs %s", d.Mount,
			human(d.FreeBytes), human(p.NeedBytes)), "free_bytes", d.FreeBytes, "need_bytes", p.NeedBytes))
	} else if after := d.FreeBytes - p.NeedBytes; after < tightBelow {
		p.Warnings = append(p.Warnings, note("disk_tight", fmt.Sprintf("only %s would be left free on %s: the "+
			"database and its backups grow with the library", human(after), d.Mount), "free_after_bytes", after))
	}
	if err := writable(p.Target); err != nil {
		p.problem(note("target_unwritable", "cannot write in "+nearestDir(p.Target)+": "+err.Error(), "error", err.Error()))
	}
	return p
}

func orWord(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// human is a size in the plan's lines.
func human(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	}
}

// Options are a move's hooks.
type Options struct {
	// Log gets a line per step (nil: none).
	Log io.Writer
	// Progress gets the bytes copied so far and the total.
	Progress func(done, total uint64)
	// Busy says why the install's data cannot be copied now (a server or its
	// database runs); nil error: nothing runs.
	Busy func(from Layout) error
	// VerifyPostgres starts the database on its copy in root/postgres and asks it a
	// question (nil: not checked, as for an install without the bundled Postgres).
	VerifyPostgres func(ctx context.Context, root string) error
	// Now is the clock (tests).
	Now func() time.Time
	// By is who asked (the record's).
	By string
}

func (o Options) logf(format string, a ...any) {
	if o.Log != nil {
		_, _ = fmt.Fprintf(o.Log, "[storage] "+format+"\n", a...)
	}
}

// Move moves in's data to target: under the move lock, it copies every folder,
// verifies the copy (every file there with its size; the database starts on it),
// points server.toml's data_dir at it, and keeps the old folders aside as
// <folder>.moved-<stamp> (never removed: Result.Old says where). A move cut short
// is finished by running it again with the same target; until server.toml is
// pointed at the copy the install runs from where it was.
func Move(ctx context.Context, in Install, target string, o Options) (Result, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	release, err := config.LockMove(in.ConfigDir, lockWait)
	if err != nil {
		return Result{}, err
	}
	defer release()
	plan := PlanMove(in, target)
	if !plan.OK {
		return Result{}, &PlanError{Plan: plan}
	}
	jpath := filepath.Join(in.ConfigDir, JournalFile)
	var j journal
	if plan.Resume {
		if !readJSON(jpath, &j) {
			return Result{}, errors.New("the unfinished move's journal cannot be read: " + jpath)
		}
		j.Entries = plan.Entries
		o.logf("finishing the move to %s that was cut short (%s)", j.Target, j.Phase)
	} else {
		j = journal{Target: plan.Target, From: plan.From, To: plan.To, Stamp: o.Now().UTC().Format("20060102-150405"),
			Phase: phaseCopying, Entries: plan.Entries, Started: o.Now().UTC()}
	}
	if j.Phase == phaseCopying && o.Busy != nil {
		if err := o.Busy(j.From); err != nil {
			return Result{}, err
		}
	}
	if !plan.Resume {
		if err := writeJSON(jpath, j); err != nil {
			return Result{}, err
		}
	}
	res := Result{Target: j.Target, From: j.From}
	for _, e := range j.Entries {
		res.Bytes += e.Bytes
		res.Files += e.Files
	}
	if j.Phase == phaseCopying {
		if err := copyAll(ctx, j, res.Bytes, o); err != nil {
			return res, err
		}
		if err := verifyAll(j, o); err != nil {
			return res, err
		}
		if o.VerifyPostgres != nil && exists(filepath.Join(j.To.Root, "postgres", "PG_VERSION")) {
			o.logf("starting the database on its copy in %s", filepath.Join(j.To.Root, "postgres"))
			if err := o.VerifyPostgres(ctx, j.To.Root); err != nil {
				return res, fmt.Errorf("the database does not start on its copy in %s (nothing was changed: "+
					"Maktaba still runs from %s): %w", filepath.Join(j.To.Root, "postgres"), j.From.Data, err)
			}
		}
		j.Phase = phaseVerified
		if err := writeJSON(jpath, j); err != nil {
			return res, err
		}
	}
	if j.Phase == phaseVerified {
		by := o.By
		if by == "" {
			by = "move"
		}
		if err := config.MarkStorage(in.ConfigDir, j.To.Data, by); err != nil {
			return res, fmt.Errorf("mark %s: %w", j.To.Data, err)
		}
		if err := config.SetDataDir(in.ConfigPath, j.Target); err != nil {
			return res, fmt.Errorf("point %s at %s: %w", in.ConfigPath, j.Target, err)
		}
		o.logf("server.toml now says data_dir = %q", j.Target)
		j.Phase = phaseSwitched
		if err := writeJSON(jpath, j); err != nil {
			return res, err
		}
	}
	old, err := retire(j)
	res.Old = old
	if err != nil {
		return res, err
	}
	res.OK, res.At = true, o.Now().UTC()
	if err := writeJSON(filepath.Join(in.ConfigDir, ResultFile), res); err != nil {
		return res, err
	}
	_ = DropRequest(in.ConfigDir)
	if err := os.Remove(jpath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	return res, nil
}

// copyAll copies every entry, saying how far it came.
func copyAll(ctx context.Context, j journal, total uint64, o Options) error {
	var done uint64
	report := func(n uint64) {
		done += n
		if o.Progress != nil {
			o.Progress(done, total)
		}
	}
	for _, e := range j.Entries {
		o.logf("copying %s (%s) to %s", e.From, human(e.Bytes), e.To)
		if err := mirror(ctx, e, report); err != nil {
			return fmt.Errorf("copy %s to %s: %w", e.From, e.To, err)
		}
	}
	return nil
}

// verifyAll checks every entry's copy.
func verifyAll(j journal, o Options) error {
	for _, e := range j.Entries {
		files, err := verify(e)
		if err != nil {
			return fmt.Errorf("the copy of %s in %s is not whole (nothing was changed: Maktaba still runs from %s): %w",
				e.From, e.To, j.From.Data, err)
		}
		o.logf("verified %s: %d files", e.To, files)
	}
	return nil
}

// retire keeps the old folders aside as <folder>.moved-<stamp>: the data folder
// whole when the root's folders are in it, else each one (state/ stays: Keep).
func retire(j journal) ([]string, error) {
	var froms []string
	if j.From.Data == j.From.Root {
		froms = []string{j.From.Data}
	} else {
		froms = append(froms, j.From.Data)
		for _, n := range config.StateRootFolders {
			if n != "state" { // where the desktop app keeps files of its own (Entry.Keep)
				froms = append(froms, filepath.Join(j.From.Root, n))
			}
		}
	}
	var old []string
	for _, f := range froms {
		dst := f + movedInfix + j.Stamp
		switch {
		case exists(dst):
			old = append(old, dst) // a run cut short kept it aside already
		case exists(f):
			if err := os.Rename(f, dst); err != nil {
				return old, fmt.Errorf("keep %s aside as %s: %w", f, dst, err)
			}
			old = append(old, dst)
		}
	}
	sort.Strings(old)
	return old, nil
}

// OldCopies are the old copies earlier moves kept aside in the folders of l and
// of the result's From (to remove by hand).
func OldCopies(dirs ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(d)), "*"+movedInfix+"*"))
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}
