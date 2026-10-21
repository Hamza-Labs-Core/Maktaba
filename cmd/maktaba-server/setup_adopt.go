package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// adoptSys is the machine the adopt step looks at (tests give it a fake).
var adoptSys = adopt.RealSys

// adoptVerifyWait is how long the verify action waits for the app's server
// by default.
const adoptVerifyWait = 2 * time.Minute

// appIdentifier is the desktop app's bundle identifier (tauri.conf.json): its
// window keeps mode.json in %LOCALAPPDATA%\<it> on Windows (mode.rs).
const appIdentifier = "com.hamzalabs.maktaba"

// adoptTarget is the app's side of a move: its server.toml, state root, keys
// and node config (on Windows the new place, story 38.1, whatever an old
// install still reads), its own install (the payload, never stopped as the old
// install's), and on Windows the app window's mode.json.
func adoptTarget(cfg config.Config) adopt.Target {
	tgt := adopt.Target{ConfigPath: config.Path(), StateDir: cfg.StateDir(), KeysDir: config.KeysDir(),
		NodeConfig: config.LocalNodeConfigPath()}
	tgt.AppPayload, _ = setupPayload()
	if runtime.GOOS == "windows" {
		home, _ := os.UserHomeDir()
		root := config.WindowsRoot(os.Getenv, home) // %LOCALAPPDATA%\Maktaba
		tgt.ModeFile = filepath.Join(filepath.Dir(root), appIdentifier, "mode.json")
	}
	return tgt
}

// adoptLog is <logs>/adopt.log, beside the step's own log.
func (s *setupRun) adoptLog() (io.Writer, func()) {
	dir := config.LogsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return s.log, func() {}
	}
	f, err := os.OpenFile(filepath.Join(dir, "adopt.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return s.log, func() {}
	}
	return io.MultiWriter(f, s.log), func() { _ = f.Close() }
}

// stepAdopt moves a Maktaba this PC already runs into the app (Epic 38 story
// 38.13, move-an-install.md): {"action": "find"} lists what runs and the move
// under way, "plan" (with "source": its id) says what a move does, "move" makes
// it (the result carries the urls finish gives: the app then starts its
// server), "verify" checks the app's server and removes the old task, "undo"
// runs the old install again (a move that stopped part way included).
func stepAdopt(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		Action  string `json:"action"`
		Source  string `json:"source"`
		WaitSec int    `json:"wait_sec"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	sys := adoptSys()
	tgt := adoptTarget(s.cfg)
	payload := tgt.AppPayload
	switch req.Action {
	case "find":
		srcs, err := adopt.Find(sys, payload)
		if err != nil {
			return nil, failStep("adopt_find", "", "Looking for Maktaba on this PC failed: %v", err)
		}
		if srcs == nil {
			srcs = []adopt.Source{}
		}
		out := map[string]any{"sources": srcs}
		if rec, ok, err := adopt.ReadRecord(tgt.StateDir); err == nil && ok {
			out["move"] = rec
		}
		// a node config nothing here runs: a fresh setup is a new home beside it
		node := false
		for _, src := range srcs {
			node = node || src.DB == adopt.DBNode
		}
		if home, path, ok := nodeLeftover(); ok && !node {
			out["leftover_node"] = map[string]string{"home": home, "path": path}
		}
		return out, nil
	case "plan", "move":
		src, err := adoptSource(sys, payload, tgt, req.Source)
		if err != nil {
			return nil, err
		}
		plan, err := adopt.MakePlan(sys, src, tgt)
		if err != nil {
			return nil, err
		}
		if req.Action == "plan" {
			return plan, nil
		}
		if len(plan.Problems) > 0 {
			return nil, failStep("adopt_blocked", "", "%s", plan.ProblemsText())
		}
		log, closeLog := s.adoptLog()
		defer closeLog()
		_, _ = fmt.Fprintf(log, "%s moving %s (%s) into the app\n", time.Now().Format(time.RFC3339), src.ID, src.Kind)
		venv := func(context.Context) error {
			_, err := stepVenv(s, nil)
			return err
		}
		rec, err := adopt.Move(s.ctx, sys, plan, tgt, venv, func(stage, message string) {
			_, _ = fmt.Fprintf(log, "%s %s: %s\n", time.Now().Format(time.RFC3339), stage, message)
			s.progress(stage, message, 0, 0)
		})
		if err != nil {
			var se *stepError
			if errors.As(err, &se) {
				return nil, err // the venv's own failure
			}
			_, _ = fmt.Fprintf(log, "%s failed: %v\n", time.Now().Format(time.RFC3339), err)
			return nil, adoptFailure(err)
		}
		cfg, _, err := config.LoadFrom(tgt.ConfigPath)
		if err != nil {
			return nil, err
		}
		_ = os.Remove(s.pendingPath()) // what a fresh setup collected does not apply
		return map[string]any{
			"move":           rec,
			"web_url":        "http://" + supervisor.LocalAddr(cfg.Server.WebListen, "8088"),
			"supervisor_url": supervisor.StateURL(cfg),
		}, nil
	case "undo":
		err := adopt.Undo(sys, tgt, func(stage, message string) { s.progress(stage, message, 0, 0) })
		if err != nil {
			var ae *adopt.Error
			if errors.As(err, &ae) {
				return nil, adoptFailure(err)
			}
			return nil, failStep("adopt_undo", "", "%v", err)
		}
		rec, _, err := adopt.ReadRecord(tgt.StateDir)
		if err != nil {
			return nil, err
		}
		return map[string]any{"move": rec}, nil
	case "verify":
		wait := adoptVerifyWait
		if req.WaitSec > 0 {
			wait = time.Duration(req.WaitSec) * time.Second
		}
		if err := adopt.Verify(s.ctx, sys, tgt, s.cfg, wait); err != nil {
			return nil, failStep("adopt_verify", "", "%v", err)
		}
		if _, err := adopt.Finish(sys, tgt.StateDir); err != nil {
			return nil, failStep("adopt_verify", "", "%v", err)
		}
		rec, _, err := adopt.ReadRecord(tgt.StateDir)
		if err != nil {
			return nil, err
		}
		return map[string]any{"move": rec}, nil
	}
	return nil, failStep("bad_input", "action", "The action is find, plan, move, verify or undo.")
}

// adoptFailure is a move's failure as the screens say it: its own code and
// params when it has them (adopt.Error), `interrupted` for a stop (the next run
// goes on), else adopt_failed; the English always in the message (Details).
func adoptFailure(err error) *stepError {
	var ae *adopt.Error
	switch {
	case errors.As(err, &ae):
		se := failStep(ae.Code, "", "%v", err)
		for k, v := range ae.Params {
			se = se.with(k, v)
		}
		return se
	case errors.Is(err, context.Canceled):
		return failStep("interrupted", "", "The move was stopped part way: %v", err)
	}
	return failStep("adopt_failed", "", "%v", err)
}

// adoptSource is the source with id as Find sees it now; one the move already
// stopped (a server started by hand is gone then) is the record's.
func adoptSource(sys adopt.Sys, payload string, tgt adopt.Target, id string) (adopt.Source, error) {
	srcs, err := adopt.Find(sys, payload)
	if err != nil {
		return adopt.Source{}, failStep("adopt_find", "", "Looking for Maktaba on this PC failed: %v", err)
	}
	for _, s := range srcs {
		if s.ID == id {
			return s, nil
		}
	}
	if rec, ok, err := adopt.ReadRecord(tgt.StateDir); err == nil && ok && rec.Source.ID == id && rec.Stage == adopt.StageStopped {
		return rec.Source, nil
	}
	return adopt.Source{}, failStep("adopt_gone", "source", "%s no longer runs on this PC.", id)
}

// nodeHere is why a fresh setup cannot run on this machine: an install outside
// the app (Find, its task enabled or not, running or not) is a node of a home.
// The app would read the same node config (or the copy an undone move left in
// %LOCALAPPDATA%) and run as a second copy of that node, so the node moves into
// the app instead. The terminal setup of that very install (this maktaba-server
// is its own, outside any app) is not a second copy. One that cannot be looked
// for is not taken as none. Empty code: nothing stands in the way.
func nodeHere() (code, message string, kv []any) {
	payload, _ := setupPayload()
	srcs, err := adopt.Find(adoptSys(), payload)
	if err != nil {
		return "install_unknown", fmt.Sprintf("Maktaba could not look at what this machine runs (%v), so it cannot tell whether a Maktaba node runs here.", err), nil
	}
	self, _ := os.Executable()
	moved := movedIn()
	for _, src := range srcs {
		if src.DB != adopt.DBNode || (payload == "" && self != "" && src.HasProgram(self)) || src.ID == moved {
			continue
		}
		return "node_here", fmt.Sprintf("This machine runs a node of the home %s outside the app (%s): the app would be a second copy of it. Move it into the app instead.",
			src.Home, src.ID), []any{"home", src.Home, "source", src.ID}
	}
	return "", "", nil
}

// installHere reports whether an install outside the app stands where the
// app's own setup would: a node of a home (nodeHere), or one that reads the
// app's own server.toml (an install on the default paths from before Epic 38,
// whose config a newer command copied into the app's place, story 38.1). The
// status step does not count the app as set up then: its server would start
// over that install with its config and identity; the welcome screen offers
// the move. One that cannot be looked for counts as there.
func installHere() bool {
	if code, _, _ := nodeHere(); code != "" {
		return true
	}
	payload, _ := setupPayload()
	srcs, err := adopt.Find(adoptSys(), payload)
	if err != nil {
		return true
	}
	moved := movedIn()
	for _, src := range srcs {
		if src.ID != moved && src.Config != "" && strings.EqualFold(filepath.Clean(src.Config), filepath.Clean(config.Path())) {
			return true
		}
	}
	return false
}

// movedIn is the install the move recorded here has stopped and moved into the
// app (its task off until the move finishes, then gone): no longer an install
// outside the app. "" when none.
func movedIn() string {
	cfg, _, err := config.Load()
	if err != nil {
		return ""
	}
	rec, ok, err := adopt.ReadRecord(cfg.StateDir())
	if err != nil || !ok {
		return ""
	}
	switch rec.Stage {
	case adopt.StageStopped, adopt.StageMoved, adopt.StageVerified, adopt.StageFinished:
		return rec.Source.ID
	}
	return ""
}

// nodeLeftover is a joined node config where the app's roles would read it
// (config.LocalNodeConfigPath) that no install here runs (nodeHere found
// none): left by an install removed, or by a move undone. A fresh setup is a
// new home beside it, pinned role = "home" (finish), never that node: a fresh
// setup asks for a home's admin and libraries, and the node it was belongs to
// another home. ok false when there is none.
func nodeLeftover() (home, path string, ok bool) {
	n := config.LocalNodeConfig()
	if !n.Joined() {
		return "", "", false
	}
	home = n.Home.Name
	if home == "" {
		home = n.HomeURL()
	}
	return home, config.LocalNodeConfigPath(), true
}
