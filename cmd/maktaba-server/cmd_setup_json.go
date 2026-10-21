package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// setup --json --step <name> (Epic 36 story 36.6, specs/epics/36-mac-app/contract-setup.md):
// one step per run, one JSON object on stdin, NDJSON events on stdout, exit 0
// with a result, 1 with an error, 2 for usage. Here so far: `status` (it lets
// the app adopt a Mac that is set up already, one moved in by migrate-from-dev)
// and `llm` (the language model the LLM stages use); the other steps answer
// unknown_step.

type setupEvent struct {
	Type    string `json:"type"`
	Data    any    `json:"data,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Field   string `json:"field,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
}

// llmSetup is the setup step's work (tests replace it).
var llmSetup = llmserver.Setup

// runSetupLLM: the `llm` step. Input {"models": [...]?, "check_only": bool?};
// progress check → start → download (bytes); result llmserver.Result. Errors
// llm_unavailable, disk_full, llm_failed.
func runSetupLLM(input map[string]any, enc *json.Encoder, fail func(int, setupEvent) int) int {
	cfg, _, err := config.Load()
	if err != nil {
		return fail(1, setupEvent{Code: "config", Message: err.Error()})
	}
	var models []string
	if raw, ok := input["models"]; ok && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return fail(2, setupEvent{Code: "bad_input", Message: "models must be a list of model names", Field: "models"})
		}
		for _, m := range list {
			s, ok := m.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return fail(2, setupEvent{Code: "bad_input", Message: "models must be a list of model names", Field: "models"})
			}
			models = append(models, strings.TrimSpace(s))
		}
	}
	checkOnly, _ := input["check_only"].(bool)
	exe, _ := os.Executable()
	logw := io.Writer(os.Stderr)
	if f, err := openLLMLog(); err == nil {
		defer func() { _ = f.Close() }()
		logw = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := llmSetup(ctx, llmserver.SetupOptions{
		Config:        cfg,
		Exe:           exe,
		Models:        models,
		SupervisorURL: supervisor.StateURL(cfg),
		CheckOnly:     checkOnly,
		Log:           logw,
		Progress: func(e llmserver.Event) {
			_ = enc.Encode(setupEvent{Type: "progress", Stage: e.Stage, Message: e.Message, Done: e.Done, Total: e.Total})
		},
	})
	switch {
	case errors.Is(err, llmserver.ErrUnavailable):
		return fail(1, setupEvent{Code: "llm_unavailable", Message: err.Error()})
	case errors.Is(err, llmserver.ErrDiskFull):
		return fail(1, setupEvent{Code: "disk_full", Message: err.Error()})
	case err != nil:
		return fail(1, setupEvent{Code: "llm_failed", Message: err.Error()})
	}
	_ = enc.Encode(setupEvent{Type: "result", Data: res})
	return 0
}

// openLLMLog appends to the model server's log in the logs directory.
func openLLMLog() (*os.File, error) {
	p := filepath.Join(config.LogsDir(), llmserver.LogName)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// runSetupJSON runs one step and returns the exit code.
func runSetupJSON(step string, in io.Reader, out io.Writer) int {
	enc := json.NewEncoder(out)
	fail := func(code int, ev setupEvent) int {
		ev.Type = "error"
		_ = enc.Encode(ev)
		return code
	}
	var input map[string]any
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return fail(2, setupEvent{Code: "bad_input", Message: "stdin must hold one JSON object"})
	}
	switch step {
	case "status":
		data, err := setupStatus()
		if err != nil {
			return fail(1, setupEvent{Code: "config", Message: err.Error()})
		}
		_ = enc.Encode(setupEvent{Type: "result", Data: data})
		return 0
	case "llm":
		return runSetupLLM(input, enc, fail)
	}
	return fail(2, setupEvent{Code: "unknown_step", Message: "this maktaba-server has no setup step " + step})
}

// setupStatus: this Mac is configured when server.toml exists and its database
// does (the bundled Postgres's cluster, a [database] url, or the SQLite file).
func setupStatus() (map[string]any, error) {
	cfg, found, err := config.Load()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"configured":     found && databaseExists(cfg),
		"web_url":        "http://" + supervisor.LocalAddr(cfg.Server.WebListen, "8088"),
		"supervisor_url": supervisor.StateURL(cfg),
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
