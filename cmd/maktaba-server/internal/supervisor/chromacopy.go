package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Epic 43 story 43.3: the search index's vectors move from Chroma (<StateDir>/chroma) into
// Postgres once. The copy (maktaba-pipeline copy-chroma) runs at a start beside the other
// roles while the index worker keeps writing both stores and search reads Chroma; once
// it is checked it switches search to Postgres and writes ChromaCopyDone. The next start
// deletes the Chroma folder: it is kept one start, for a rollback.

// ChromaCopyInstance is the copy's instance name (pipeline/chroma-copy).
const ChromaCopyInstance = "chroma-copy"

// ChromaCopyTries is how many failed runs in a row the copy gets at one start: it
// continues where it stopped each time, so a failure that repeats is not a passing one.
const ChromaCopyTries = 5

const (
	// ChromaCopyDone is written (in the state dir) once the copy is checked and search
	// reads Postgres; it names the folder copied.
	ChromaCopyDone = "chroma-copy.done"
	// ChromaCopyProgress is the copy's progress, for /processes.
	ChromaCopyProgress = "chroma-copy.json"
)

// chromaDir is the Chroma folder the pipeline's environment names (MAKTABA_CHROMA_DIR,
// by default <StateDir>/chroma).
func chromaDir(env []string) string {
	return envValue(env, "MAKTABA_CHROMA_DIR")
}

// BuildChromaCopy is the copy's process on a home with Postgres whose Chroma folder is
// there and not copied yet. On the start after a copy (ChromaCopyDone names the folder)
// it deletes the folder instead, in the background, and builds nothing.
func BuildChromaCopy(opts Options) ([]Process, error) {
	n, err := NodeOf(opts.Config)
	if err != nil {
		return nil, err
	}
	if NodeMode(opts.Config, n) {
		return nil, nil // the index is the home's
	}
	base, err := ChildEnv(RolePipeline, opts.Config)
	if err != nil {
		return nil, err
	}
	env := append(os.Environ(), base...)
	dir := chromaDir(env)
	url := envValue(env, "MAKTABA_DATABASE_URL")
	if dir == "" || !(strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")) {
		return nil, nil // SQLite: no semantic search to move
	}
	state := opts.Config.StateDir()
	done := filepath.Join(state, ChromaCopyDone)
	if copied, ok := chromaCopied(done); ok {
		if filepath.Clean(copied) == filepath.Clean(dir) {
			go removeChroma(dir, filepath.Join(state, ChromaCopyProgress))
		}
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "chroma.sqlite3")); err != nil {
		return nil, nil // nothing to copy
	}
	progress := filepath.Join(state, ChromaCopyProgress)
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
		return "", true // a done copy all the same: never copied again, nothing deleted
	}
	return d.ChromaDir, true
}

// removeChroma deletes the copied Chroma folder (42 GB on the home Mac).
func removeChroma(dir, progress string) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	start := time.Now()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "[supervisor] the search index was copied into the database; deleting %s failed: %v\n", dir, err)
		return
	}
	_ = os.Remove(progress)
	fmt.Fprintf(os.Stderr, "[supervisor] the search index was copied into the database: deleted %s (%s)\n",
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
