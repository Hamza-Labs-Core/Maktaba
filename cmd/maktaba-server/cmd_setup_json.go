package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// setup --json --step <name> (Epic 36 story 36.6, specs/epics/36-mac-app/contract-setup.md):
// one step per run, one JSON object on stdin, NDJSON events on stdout, exit 0
// with a result, 1 with an error, 2 for usage. Only `status` is here so far: it
// lets the app adopt a Mac that is set up already (one moved in by
// migrate-from-dev); the other steps answer unknown_step.

type setupEvent struct {
	Type    string `json:"type"`
	Data    any    `json:"data,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Field   string `json:"field,omitempty"`
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
