package updater

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	// RequestKindServer: the app holds the release already (a retry after its rollback:
	// the launcher ran the copy of the release before); the app shows it, and at the go
	// the server starts it (the launcher runs the app's own payload again)
	RequestKindServer = "server"
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
		code, detail := appFailure(q.Error)
		return "", Fail(code, "%s", detail)
	}
	return q.State, nil
}

// appCodes are the app's own codes for an order it could not install, as its error
// begins ("<code>: <why>"): needs_admin (an app this user cannot replace: a person moves
// it or an administrator installs it), local_build (an app built locally, with the
// placeholder updater key: it can verify no update; the release is installed by hand
// once), app_management (macOS does not let it replace itself: App Management, locked
// files), translocated (macOS runs it from a temporary read-only place: it is moved to
// Applications), not_writable (its files cannot be replaced), installer_not_finished
// (the Windows installer did not finish once the app closed: tried again).
var appCodes = []string{"needs_admin", "local_build", "app_management", "translocated", "not_writable",
	"installer_not_finished"}

// refusedCodes are the app's codes for an order it refused before it touched its bundle
// (a person's to mend; the release before needs no copy put back).
var refusedCodes = map[string]bool{"needs_admin": true, "local_build": true, "app_management": true,
	"translocated": true, "not_writable": true}

// appFailure is the code and detail of the app's error: one of appCodes, else
// app_install_failed.
func appFailure(msg string) (string, string) {
	for _, c := range appCodes {
		if rest, ok := strings.CutPrefix(msg, c+":"); ok {
			return c, strings.TrimSpace(rest)
		}
	}
	return "app_install_failed", msg
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

// failure is the failure the app reported for the run's order (nil: none).
func (r requests) failure(j *Journal) *Failure {
	q, err := r.read()
	if err != nil || q.RunID != j.Job.RunID || q.State != RequestFailed {
		return nil
	}
	code, detail := appFailure(q.Error)
	return &Failure{Step: StepRestart, Code: code, Detail: detail}
}

// nudger starts the app with the order's deep link when it has not answered for a
// while (once per after: a running app takes the link too). Used from one goroutine.
type nudger struct {
	run string
	at  time.Time
}

func (n *nudger) nudge(r requests, j *Journal, now time.Time, after time.Duration, open func(context.Context, string) error) {
	if open == nil {
		return
	}
	if n.run != j.Job.RunID {
		n.run, n.at = j.Job.RunID, now
		if q, err := r.read(); err == nil && !q.At.IsZero() {
			n.at = q.At
		}
	}
	if now.Sub(n.at) >= after {
		n.at = now
		_ = open(context.Background(), AppInstallURL+"?run="+j.Job.RunID)
	}
}

// remove withdraws any order (the app forgets what it staged).
func (r requests) remove() { _ = os.Remove(r.path()) }
