package updater

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Asker is an install a person updates (owner decision D1, install-kinds.md "Ask
// first"): a desktop app that runs the server — Maktaba.app, the Windows app, the Linux
// app's runtime — installs the release the home orders only when someone clicks
// "Install" on that machine. Apply orders the release (Ask: the app stages it and shows
// it); the machine waits for the click, then for the home's go (the home drains its
// workers first; a node waits its turn), and only then switches.
type Asker interface {
	// Asks reports whether this install waits for a click (a Linux runtime the app owns
	// does, a headless one does not).
	Asks() bool
	// Ask orders the release from the app; asked again for the same run, it leaves the
	// app's answer as it is.
	Ask(j *Journal) error
	// Answer is the app's answer to the run's order: RequestOrdered (not staged yet),
	// RequestStaged, RequestClicked, or an error when it failed.
	Answer(j *Journal) (string, error)
}

// AppRequestFile is the order the server and the desktop app share, in the launcher
// root's updates directory (the app's own state folder): app-install.json.
const AppRequestFile = "app-install.json"

// The order's states: who writes each, and what it means.
const (
	// RequestOrdered (server): the home ordered this release. The app stages it (kind
	// app: its own update from Feed, verified with its key; kind runtime: nothing to
	// fetch, the server has it) and shows it ("Install …").
	RequestOrdered = "ordered"
	// RequestStaged (app): staged and shown; it waits for a click.
	RequestStaged = "staged"
	// RequestClicked (app): someone chose to install it.
	RequestClicked = "clicked"
	// RequestGo (server, kind app): the home's run is ready; the app installs what it
	// staged (verified again) and the server restarts on it.
	RequestGo = "go"
	// RequestInstalling (app, or the server switching its runtime).
	RequestInstalling = "installing"
	// RequestFailed (app): Error says why; nothing was installed.
	RequestFailed = "failed"
)

// Kinds of order (AppRequest.Kind).
const (
	RequestKindApp     = "app"     // the app's own updater installs it (Mac, Windows)
	RequestKindRuntime = "runtime" // the server installs it in its runtime (Linux app)
)

// AppRequest is app-install.json.
type AppRequest struct {
	RunID   string `json:"run_id"`
	Version string `json:"version"`
	// Feed is the release's own updater feed (releases/download/v<v>/latest.json): the
	// app stages exactly this release (kind app).
	Feed string `json:"feed,omitempty"`
	Kind string `json:"kind"`
	// Home: this machine is the home (the app says the home ordered it; on a node, that
	// the cluster's update waits for this machine).
	Home  bool      `json:"home,omitempty"`
	State string    `json:"state"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

// requests is the order file in an updates directory.
type requests struct {
	dir string
	now func() time.Time
}

func (r requests) path() string { return filepath.Join(r.dir, AppRequestFile) }

func (r requests) read() (AppRequest, error) {
	var q AppRequest
	b, err := os.ReadFile(r.path())
	if err != nil {
		return q, err
	}
	return q, json.Unmarshal(b, &q)
}

func (r requests) write(q AppRequest) error {
	q.At = r.now().UTC()
	b, _ := json.MarshalIndent(q, "", "  ")
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	return layout.WriteFileAtomic(r.path(), append(b, '\n'), 0o600)
}

// order writes the run's order unless the file holds it already (the app's answer stays).
func (r requests) order(j *Journal, kind, feed string) error {
	if q, err := r.read(); err == nil && q.RunID == j.Job.RunID {
		return nil
	}
	if err := r.write(AppRequest{RunID: j.Job.RunID, Version: layout.NormalizeVersion(j.Job.Version), Feed: feed,
		Kind: kind, Home: j.Job.Home, State: RequestOrdered}); err != nil {
		return Fail("app_request_failed", "%v", err)
	}
	return nil
}

// answer is the app's answer to the run's order.
func (r requests) answer(j *Journal) (string, error) {
	q, err := r.read()
	if errors.Is(err, fs.ErrNotExist) {
		return "", Fail("app_order_gone", "the order of %s is gone", j.Job.Version)
	}
	if err != nil {
		return RequestOrdered, nil // being rewritten: asked again
	}
	if q.RunID != j.Job.RunID {
		return "", Fail("app_order_gone", "the order of %s was replaced", j.Job.Version)
	}
	if q.State == RequestFailed {
		return "", Fail("app_install_failed", "%s", q.Error)
	}
	return q.State, nil
}

// set moves the run's order to state (written afresh when it is gone).
func (r requests) set(j *Journal, kind, feed, state string) error {
	q, err := r.read()
	if err != nil || q.RunID != j.Job.RunID {
		q = AppRequest{RunID: j.Job.RunID, Version: layout.NormalizeVersion(j.Job.Version), Feed: feed, Kind: kind,
			Home: j.Job.Home}
	}
	q.State, q.Error = state, ""
	return r.write(q)
}

// remove withdraws any order (the app forgets what it staged).
func (r requests) remove() { _ = os.Remove(r.path()) }
