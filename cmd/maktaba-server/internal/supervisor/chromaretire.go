package supervisor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 43 story 43.5: Chroma is retired. Semantic search reads and writes Postgres
// (segment_embeddings, pgvector) only, and nothing opens the Chroma folder
// (<StateDir>/chroma, or the MAKTABA_CHROMA_DIR an operator set) any more. The releases
// before 43.5 copied its vectors into Postgres once (story 43.3: `copy-chroma`, which
// wrote ChromaCopyDone) and deleted the folder at the start after. This release deletes
// what is left:
//
//   - a folder whose copy is done (ChromaCopyDone names it): at this start;
//   - a folder never copied (or copied in part): the first start that finds it keeps it
//     and marks it (ChromaRetired), so a rollback to the release before finds it as it
//     left it (that release searches it and goes on copying it). A start ChromaKeep or
//     more after the mark deletes it: past the update's watch and any rollback, however
//     many times the release started meanwhile. Its videos have no vectors in Postgres,
//     count as not indexed, and are indexed again (the reconciler queues them).
//
// Nothing here can stop a start: a folder that cannot be read or deleted is said in the
// log and left.

const (
	// ChromaCopyDone is what the copy of the releases before 43.5 wrote (in the state dir)
	// once it was checked; it names the folder copied.
	ChromaCopyDone = "chroma-copy.done"
	// ChromaCopyProgress was the copy's progress, for /processes.
	ChromaCopyProgress = "chroma-copy.json"
	// ChromaRetired marks the start that kept a folder never copied (JSON: the folder,
	// when).
	ChromaRetired = "chroma-retired"
	// ChromaKeep is how long a folder never copied is kept after its mark
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
// it, else <StateDir>/chroma.
func ChromaDir(cfg config.Config) string {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_CHROMA_DIR")); v != "" {
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

// removeChroma deletes the Chroma folder and the copy's progress and retirement marks.
func removeChroma(dir, state string, w io.Writer) {
	start := time.Now()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(w, "[supervisor] the search index is in the database; deleting %s (Chroma) failed: %v\n", dir, err)
		return
	}
	_ = os.Remove(filepath.Join(state, ChromaCopyProgress))
	_ = os.Remove(filepath.Join(state, ChromaRetired))
	fmt.Fprintf(w, "[supervisor] the search index is in the database: deleted %s (Chroma) in %s\n",
		dir, time.Since(start).Round(time.Millisecond))
}
