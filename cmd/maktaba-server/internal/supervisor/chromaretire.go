package supervisor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 43 story 43.5: Chroma is retired. Semantic search reads and writes Postgres
// (segment_embeddings, pgvector) only, and nothing opens Chroma any more. The releases
// before 43.5 copied its vectors into Postgres once (story 43.3: `copy-chroma`, which
// wrote ChromaCopyDone) and deleted the folder at the start after. This release keeps
// that order, and finishes what they left:
//
//   - a home on Postgres whose Chroma folder (<StateDir>/chroma, or the
//     MAKTABA_CHROMA_DIR an operator set) is there and not copied runs the copy at its
//     start (BuildChromaCopy: `copy-chroma`, which reads Chroma's files, never Chroma),
//     from where an earlier start left it; every child is told so
//     (MAKTABA_CHROMA_COPY=pending), and nothing indexes a video again meanwhile: no
//     vector is embedded twice. The folder stays until the copy is done;
//   - a folder whose copy is done (ChromaCopyDone names it) loses Chroma's files at the
//     start after the copy (kept that one start for a rollback, as before);
//   - a home with no Postgres (SQLite: Chroma was its store, and there is no pgvector to
//     copy into) keeps the folder for a day after the first start of this release that
//     kept it (ChromaRetired marks it, ChromaKeep), for a rollback, then deletes it.
//
// Only Chroma's own entries are deleted (chroma.sqlite3 and its -wal/-shm/-journal, and
// the segment folders Chroma names by UUID), then the folder if that left it empty: a
// MAKTABA_CHROMA_DIR an operator pointed at a folder holding other things (the state dir
// itself) loses nothing else. Nothing here can stop a start: a folder that cannot be
// read or deleted is said in the log and left.

// ChromaCopyInstance is the copy's instance name (pipeline/chroma-copy).
const ChromaCopyInstance = "chroma-copy"

// ChromaCopyTries is how many failed runs in a row the copy gets at one start: it
// continues where it stopped each time, so a failure that repeats is not a passing one.
const ChromaCopyTries = 5

// EnvChromaCopy tells every child that this start runs the copy ("pending"): the
// pipeline then indexes nothing again for want of vectors (search/pgstore.py).
const EnvChromaCopy = "MAKTABA_CHROMA_COPY"

const (
	// ChromaCopyDone is written (in the state dir) once the copy is checked and the
	// state row switched; it names the folder copied (the releases before 43.5 wrote it
	// too).
	ChromaCopyDone = "chroma-copy.done"
	// ChromaCopyProgress is the copy's progress, for /processes.
	ChromaCopyProgress = "chroma-copy.json"
	// ChromaRetired marks the start that kept a SQLite home's folder (JSON: the folder,
	// when).
	ChromaRetired = "chroma-retired"
	// ChromaKeep is how long a SQLite home's folder is kept after its mark
	// (MAKTABA_CHROMA_KEEP_SECONDS changes it: the rehearsal's).
	ChromaKeep = 24 * time.Hour
)

// chromaKeep is ChromaKeep, or MAKTABA_CHROMA_KEEP_SECONDS.
func chromaKeep() time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MAKTABA_CHROMA_KEEP_SECONDS"))); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return ChromaKeep
}

// ChromaDir is the install's Chroma folder: MAKTABA_CHROMA_DIR when the operator set
// it (a leading ~ is the home folder, as the pipeline read it), else <StateDir>/chroma.
func ChromaDir(cfg config.Config) string {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_CHROMA_DIR")); v != "" {
		if v == "~" || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, `~\`) {
			if home, err := os.UserHomeDir(); err == nil {
				v = filepath.Join(home, v[1:])
			}
		}
		return v
	}
	return filepath.Join(cfg.StateDir(), "chroma")
}

// RetireChroma deletes the Chroma folder as the comment above says, in the background
// (42 GB on the home Mac), and reports what it did on w. It returns the folder it
// deletes, or "".
func RetireChroma(cfg config.Config, w io.Writer) string {
	state := cfg.StateDir()
	dir := ChromaDir(cfg)
	retired := filepath.Join(state, ChromaRetired)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		// nothing left: the marks of the copy go too (ChromaCopyDone stays: a rollback's
		// release reads it, and never copies again)
		_ = os.Remove(retired)
		_ = os.Remove(filepath.Join(state, ChromaCopyProgress))
		return ""
	}
	if !isChromaFolder(dir) {
		fmt.Fprintf(w, "[supervisor] %s is not a Chroma folder (no chroma.sqlite3): left as it is\n", dir)
		return ""
	}
	copied, ok := chromaCopied(filepath.Join(state, ChromaCopyDone))
	keptAt, marked := keptSince(retired, dir)
	switch {
	case ok && (copied == "" || filepath.Clean(copied) == filepath.Clean(dir)):
		// the copy was checked (a done file that cannot be read is a done copy all the same)
	case copyHome(cfg):
		fmt.Fprintf(w, "[supervisor] %s (Chroma) is not copied into the database yet: the copy runs now; "+
			"the folder goes at the start after it is done\n", dir)
		return ""
	case marked && time.Since(keptAt) >= chromaKeep():
	case marked:
		fmt.Fprintf(w, "[supervisor] %s (Chroma, not copied) is no longer used; kept since %s for a rollback, "+
			"deleted at a start from %s\n", dir, keptAt.Format(time.RFC3339), keptAt.Add(chromaKeep()).Format(time.RFC3339))
		return ""
	default:
		raw, _ := json.Marshal(map[string]any{"chroma_dir": dir, "kept_at": time.Now().UTC().Format(time.RFC3339)})
		if err := os.WriteFile(retired, raw, 0o600); err != nil {
			fmt.Fprintf(w, "[supervisor] the search index is in the database now; %s is no longer used and is kept: %v\n", dir, err)
			return ""
		}
		fmt.Fprintf(w, "[supervisor] the search index is in the database now: %s (Chroma, not copied) is no longer used; "+
			"kept for this start (a rollback reads it), deleted at a start a day from now; its videos are indexed again\n", dir)
		return ""
	}
	go removeChroma(dir, state, w)
	return dir
}

// copyHome: this machine is a home on Postgres (where the copy runs; a machine's index is
// the home's, and SQLite has no pgvector to copy into).
func copyHome(cfg config.Config) bool {
	n, err := NodeOf(cfg)
	if err != nil || NodeMode(cfg, n) {
		return false
	}
	url := strings.TrimSpace(os.Getenv("MAKTABA_DATABASE_URL"))
	if url == "" {
		url = cfg.Database.URL
	}
	return strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")
}

// ChromaCopyPending reports whether this start runs the copy: a home on Postgres whose
// Chroma folder has Chroma's database and is not the one ChromaCopyDone names.
func ChromaCopyPending(cfg config.Config) bool {
	dir := ChromaDir(cfg)
	if _, err := os.Stat(filepath.Join(dir, "chroma.sqlite3")); err != nil {
		return false
	}
	if copied, ok := chromaCopied(filepath.Join(cfg.StateDir(), ChromaCopyDone)); ok &&
		(copied == "" || filepath.Clean(copied) == filepath.Clean(dir)) {
		return false
	}
	return copyHome(cfg)
}

// BuildChromaCopy is the copy's process when ChromaCopyPending: `copy-chroma` with the
// pipeline's environment, the folder, the done file and the progress file.
func BuildChromaCopy(opts Options) ([]Process, error) {
	if !ChromaCopyPending(opts.Config) {
		return nil, nil
	}
	base, err := ChildEnv(RolePipeline, opts.Config)
	if err != nil {
		return nil, err
	}
	state := opts.Config.StateDir()
	done := filepath.Join(state, ChromaCopyDone)
	progress := filepath.Join(state, ChromaCopyProgress)
	env := append(os.Environ(), base...)
	env = setEnv(env, "MAKTABA_CHROMA_DIR", ChromaDir(opts.Config))
	env = setEnv(env, "MAKTABA_PIPELINE_SHARE", "off")
	env = setEnv(env, "MAKTABA_COPY_CHROMA_PROGRESS", progress)
	env = setEnv(env, "MAKTABA_COPY_CHROMA_DONE", done)
	cmd := exec.Command(pythonFor(opts), "-m", "maktaba_pipeline", "copy-chroma")
	cmd.Env = env
	wire(cmd)
	return []Process{{Role: RolePipeline, Instance: ChromaCopyInstance, Cmd: cmd,
		Once: true, Tries: ChromaCopyTries,
		Progress: func() Detail { return chromaCopyDetail(progress) }}}, nil
}

// isChromaFolder: Chroma's own database is there, or the folder is empty (Chroma made it
// and wrote nothing yet). Any other folder an operator named is never deleted.
func isChromaFolder(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "chroma.sqlite3")); err == nil {
		return true
	}
	ents, err := os.ReadDir(dir)
	return err == nil && len(ents) == 0
}

// keptSince: when a start of this release (or a later one) kept dir (ChromaRetired names
// it), and whether one did. A mark that cannot be read counts as made now.
func keptSince(path, dir string) (time.Time, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	var d struct {
		ChromaDir string `json:"chroma_dir"`
		KeptAt    string `json:"kept_at"`
	}
	if json.Unmarshal(raw, &d) != nil || filepath.Clean(d.ChromaDir) != filepath.Clean(dir) {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, d.KeptAt)
	if err != nil {
		return time.Now(), true
	}
	return at, true
}

// chromaCopied reads ChromaCopyDone: the folder copied, and whether the file is there.
func chromaCopied(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var d struct {
		ChromaDir string `json:"chroma_dir"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return "", true
	}
	return d.ChromaDir, true
}

// chromaEntry reports whether a folder entry is Chroma's own: its database (and the
// database's side files) or a segment folder (named by a UUID).
func chromaEntry(e os.DirEntry) bool {
	name := e.Name()
	if !e.IsDir() {
		return name == "chroma.sqlite3" || strings.HasPrefix(name, "chroma.sqlite3-")
	}
	return uuidName.MatchString(name)
}

var uuidName = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// removeChroma deletes Chroma's entries of the folder, the folder when nothing else is
// left in it, and the copy's progress and retirement marks.
func removeChroma(dir, state string, w io.Writer) {
	start := time.Now()
	ents, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(w, "[supervisor] the search index is in the database; reading %s (Chroma) failed: %v\n", dir, err)
		return
	}
	kept := 0
	for _, e := range ents {
		if !chromaEntry(e) {
			kept++
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			fmt.Fprintf(w, "[supervisor] the search index is in the database; deleting %s (Chroma) failed: %v\n",
				filepath.Join(dir, e.Name()), err)
			return
		}
	}
	if kept == 0 {
		_ = os.Remove(dir)
	} else {
		fmt.Fprintf(w, "[supervisor] %s holds %d entries that are not Chroma's: they are kept\n", dir, kept)
	}
	_ = os.Remove(filepath.Join(state, ChromaCopyProgress))
	_ = os.Remove(filepath.Join(state, ChromaRetired))
	fmt.Fprintf(w, "[supervisor] the search index is in the database: deleted %s (Chroma) in %s\n",
		dir, time.Since(start).Round(time.Millisecond))
}

// chromaCopyDetail is the copy's progress as /processes says it.
func chromaCopyDetail(path string) Detail {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Detail{}
	}
	var p struct {
		Phase    string  `json:"phase"`
		Elements int64   `json:"elements"`
		Total    int64   `json:"total"`
		Copied   int64   `json:"copied"`
		Rate     float64 `json:"rows_per_sec"`
		ETA      *int64  `json:"eta_sec"`
		Reason   string  `json:"reason"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return Detail{}
	}
	switch p.Phase {
	case "load":
		text := fmt.Sprintf("Copying the search index into the database: %d of %d", p.Elements, p.Total)
		if p.Total > 0 {
			text += fmt.Sprintf(" (%d%%)", p.Elements*100/p.Total)
		}
		if p.ETA != nil {
			text += ", about " + (time.Duration(*p.ETA) * time.Second).Round(time.Minute).String() + " left"
		}
		return Detail{Text: text}
	case "index":
		return Detail{Text: fmt.Sprintf("Building the search index in the database (%d vectors copied)", p.Copied)}
	case "verify":
		return Detail{Text: "Checking the copied search index"}
	case "failed":
		return Detail{Text: "The copied search index failed its check: " + p.Reason}
	}
	return Detail{}
}
