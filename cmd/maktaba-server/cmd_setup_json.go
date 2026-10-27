package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// setup --json --step <name> (Epic 36 story 36.6, Epic 38 story 38.6;
// specs/epics/36-mac-app/contract-setup.md): the desktop app's setup screens,
// one step per run. One JSON object on stdin, NDJSON events on stdout (any
// number of progress lines, then one result or error), exit 0 with a result,
// 1 with an error, 2 for usage. A step saves what it collected in the pending
// setup state (setupPending); `finish` applies it. Running a step again with
// the same input is safe (the user may go back a screen). Secrets arrive on
// stdin only and are never printed.

type setupEvent struct {
	Type    string `json:"type"`
	Stage   string `json:"stage,omitempty"`
	Data    any    `json:"data,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Field   string `json:"field,omitempty"`
	// Params are the values the screens put into the code's own text
	// (setup.error.<code> in the app's English and Arabic; message is the
	// English fallback, for a log or an app that does not know the code).
	Params map[string]any `json:"params,omitempty"`
	Done   int64          `json:"done,omitempty"`
	Total  int64          `json:"total,omitempty"`
}

// stepError is a step's failure as the screen shows it: a code the app maps
// to its text (with params), an English message, and the input field it
// belongs to. usage marks input the step cannot take (exit 2, as unreadable
// stdin).
type stepError struct {
	code, message, field string
	params               map[string]any
	usage                bool
}

func (e *stepError) Error() string { return e.code + ": " + e.message }

func failStep(code, field, format string, a ...any) *stepError {
	return &stepError{code: code, message: fmt.Sprintf(format, a...), field: field}
}

// with sets the error's params from key, value pairs.
func (e *stepError) with(kv ...any) *stepError {
	if e.params == nil {
		e.params = map[string]any{}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		e.params[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return e
}

// setupRun is one step's run: the config as it stands, the events out.
type setupRun struct {
	ctx   context.Context
	cfg   config.Config
	found bool
	out   *json.Encoder
	log   io.Writer
}

// progress sends a progress line (done/total: bytes or items; 0 total is an
// indeterminate bar).
func (s *setupRun) progress(stage, message string, done, total uint64) {
	_ = s.out.Encode(setupEvent{Type: "progress", Stage: stage, Message: message, Done: int64(done), Total: int64(total)})
}

type setupStepFunc func(s *setupRun, in json.RawMessage) (any, error)

// setupSteps are the steps the app runs, in screen order.
var setupSteps = map[string]setupStepFunc{
	"status":    func(*setupRun, json.RawMessage) (any, error) { return setupStatus() },
	"check":     stepCheck,
	"storage":   stepStorage,
	"admin":     stepAdmin,
	"libraries": stepLibraries,
	"models":    stepModels,
	"plex":      stepPlex,
	"venv":      stepVenv,
	"llm":       stepLLM,
	"adopt":     stepAdopt,
	"finish":    stepFinish,
}

// llmSetup is the llm step's work (tests replace it).
var llmSetup = llmserver.Setup

// stepLLM: the `llm` step (Epic 36 story 36.11), the language model the LLM
// stages use. Input {"models": [...]?, "check_only": bool?}; progress check →
// start → download (bytes); result llmserver.Result. Errors llm_unavailable,
// disk_full, llm_failed.
func stepLLM(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		Models    json.RawMessage `json:"models"`
		CheckOnly bool            `json:"check_only"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	var models []string
	if len(req.Models) > 0 && string(req.Models) != "null" {
		var list []string
		bad := &stepError{code: "bad_input", message: "models must be a list of model names", field: "models", usage: true}
		if json.Unmarshal(req.Models, &list) != nil {
			return nil, bad
		}
		for _, m := range list {
			if strings.TrimSpace(m) == "" {
				return nil, bad
			}
			models = append(models, strings.TrimSpace(m))
		}
	}
	exe, _ := os.Executable()
	logw := s.log
	if f, err := openLLMLog(); err == nil {
		defer func() { _ = f.Close() }()
		logw = f
	}
	res, err := llmSetup(s.ctx, llmserver.SetupOptions{
		Config:        s.cfg,
		Exe:           exe,
		Models:        models,
		SupervisorURL: supervisor.StateURL(s.cfg),
		CheckOnly:     req.CheckOnly,
		Log:           logw,
		Progress: func(e llmserver.Event) {
			_ = s.out.Encode(setupEvent{Type: "progress", Stage: e.Stage, Message: e.Message, Done: e.Done, Total: e.Total})
		},
	})
	switch {
	case errors.Is(err, llmserver.ErrUnavailable):
		return nil, failStep("llm_unavailable", "", "%v", err)
	case errors.Is(err, llmserver.ErrDiskFull):
		return nil, failStep("disk_full", "", "%v", err)
	case err != nil:
		return nil, failStep("llm_failed", "", "%v", err)
	}
	return res, nil
}

// openLLMLog appends to the model server's log in the logs directory.
func openLLMLog() (*os.File, error) { return config.OpenLog(llmserver.LogName) }

// runSetupJSON runs one step and returns the exit code.
func runSetupJSON(step string, in io.Reader, out io.Writer) int {
	enc := json.NewEncoder(out)
	fail := func(code int, ev setupEvent) int {
		ev.Type = "error"
		_ = enc.Encode(ev)
		return code
	}
	raw, err := io.ReadAll(io.LimitReader(in, 1<<20))
	var probe map[string]json.RawMessage
	if err != nil || json.Unmarshal(bytes.TrimSpace(raw), &probe) != nil || probe == nil {
		return fail(2, setupEvent{Code: "bad_input", Message: "stdin must hold one JSON object"})
	}
	fn, ok := setupSteps[step]
	if !ok {
		return fail(2, setupEvent{Code: "unknown_step", Message: "this maktaba-server has no setup step " + step})
	}
	cfg, found, err := config.Load()
	if err != nil {
		return fail(1, setupEvent{Code: "config", Message: err.Error()})
	}
	// a stop (the app quitting) ends a long step cleanly; venv and models
	// downloads resume at the next run
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := &setupRun{ctx: ctx, cfg: cfg, found: found, out: enc, log: os.Stderr}
	data, err := fn(s, raw)
	if err != nil {
		var se *stepError
		if errors.As(err, &se) {
			code := 1
			if se.usage {
				code = 2
			}
			return fail(code, setupEvent{Code: se.code, Message: se.message, Field: se.field, Params: se.params})
		}
		return fail(1, setupEvent{Code: "internal", Message: err.Error()})
	}
	_ = enc.Encode(setupEvent{Type: "result", Data: data})
	return 0
}

// setupStatus: this machine is configured when server.toml exists and its
// database does (the bundled Postgres's cluster, a [database] url, or the
// SQLite file), or it is a node (a joined node config: the home's database;
// an install moved into the app, story 38.13). Not after the move into the
// app was undone (adopt --undo): the old install runs this machine's Maktaba
// again, and the app must not register its server over it.
func setupStatus() (map[string]any, error) {
	cfg, found, err := config.Load()
	if err != nil {
		return nil, err
	}
	node := supervisor.NodeJoined(cfg)
	// a data folder on a disk that is not connected (Epic 36 story 36.15) is an
	// install set up: the server waits for its disk, and setup never makes a new
	// database in its place
	missing, storageMissing := cfg.StorageMissing()
	configured := found && (node || storageMissing || databaseExists(cfg)) && !adopt.UndoneHere(cfg.StateDir())
	// a server.toml and node config in the app's place that are an install's
	// outside the app (copied there by a newer command, story 38.1): not the
	// app's own while that install is here (installHere)
	if configured && installHere() {
		configured = false
	}
	return map[string]any{
		"storage_missing": missing,
		"configured":      configured,
		"web_url":         "http://" + supervisor.LocalAddr(cfg.Server.WebListen, "8088"),
		"supervisor_url":  supervisor.StateURL(cfg),
	}, nil
}

func databaseExists(cfg config.Config) bool {
	if cfg.DatabaseURLSet {
		return true
	}
	exe, _ := os.Executable()
	if b, ok, err := pgbundle.Resolve(cfg, exe); err == nil && ok {
		return b.Initialized()
	}
	if path, ok := strings.CutPrefix(cfg.Database.URL, "sqlite://"); ok {
		_, err := os.Stat(path)
		return err == nil
	}
	return false
}

// apiSub runs `maktaba-api <args>` (the payload's, beside this binary) with
// the api's environment for this config, the database at dbURL (when set)
// and stdin as given. Its stdout is returned; its stderr goes to the step's
// log (never the screen). Its exit code is returned too (-1 when it did not
// run).
func (s *setupRun) apiSub(dbURL string, stdin io.Reader, args ...string) ([]byte, int, error) {
	bin, err := supervisor.LocateAPI("")
	if err != nil {
		return nil, -1, failStep("server_missing", "", "the API program is missing: %v", err)
	}
	base, err := supervisor.ChildEnv(supervisor.RoleAPI, s.cfg)
	if err != nil {
		return nil, -1, failStep("internal", "", "%v", err)
	}
	cmd := exec.CommandContext(s.ctx, bin, args...)
	env := append(base, os.Environ()...)
	if dbURL != "" {
		env = append(env, "DATABASE_URL="+dbURL)
	}
	if env, err = dsn.ProcessEnv(env, config.KeysDir()); err != nil {
		return nil, -1, failStep("internal", "", "%v", err)
	}
	cmd.Env = env
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = s.log
	err = cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return out.Bytes(), code, err
}

// decodeInput reads a step's input into v.
func decodeInput(in json.RawMessage, v any) error {
	if err := json.Unmarshal(in, v); err != nil {
		return failStep("bad_input", "", "the step's input is not what it takes: %v", err)
	}
	return nil
}
