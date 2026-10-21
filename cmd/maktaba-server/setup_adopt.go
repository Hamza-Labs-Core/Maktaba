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

// adoptTarget is the app's side of a move: its server.toml, state root, keys
// and node config (on Windows the new place, story 38.1, whatever an old
// install still reads).
func adoptTarget(cfg config.Config) adopt.Target {
	node := config.LocalNodeConfigPath()
	if runtime.GOOS == "windows" && strings.TrimSpace(os.Getenv("MAKTABA_NODE_CONFIG")) == "" {
		home, _ := os.UserHomeDir()
		node = filepath.Join(config.WindowsRoot(os.Getenv, home), "node.json")
	}
	return adopt.Target{ConfigPath: config.Path(), StateDir: cfg.StateDir(), KeysDir: config.KeysDir(), NodeConfig: node}
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
// server), "verify" checks the app's server and removes the old task.
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
	payload, _ := setupPayload()
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
			return nil, failStep("adopt_blocked", "", "%s", strings.Join(plan.Problems, "; "))
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
			return nil, failStep("adopt_failed", "", "%v", err)
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
	case "verify":
		wait := adoptVerifyWait
		if req.WaitSec > 0 {
			wait = time.Duration(req.WaitSec) * time.Second
		}
		if err := adopt.Verify(s.ctx, tgt, s.cfg, wait); err != nil {
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
	return nil, failStep("bad_input", "action", "The action is find, plan, move or verify.")
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
