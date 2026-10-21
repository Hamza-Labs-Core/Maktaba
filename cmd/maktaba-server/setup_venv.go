package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// setupPayload is the app's server payload (bin/, python/, pipeline/,
// payload.json): MAKTABA_APP_BUNDLE when the launcher set it, else the payload
// holding this executable (the app runs setup steps with its bundled
// maktaba-server, outside the launcher).
func setupPayload() (string, error) {
	if b := config.AppBundle(); b != "" {
		return b, nil
	}
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		dir := filepath.Dir(filepath.Dir(exe)) // <payload>/bin/maktaba-server
		if _, err := os.Stat(filepath.Join(dir, "payload.json")); err == nil {
			return dir, nil
		}
	}
	return "", failStep("server_missing", "", "This maktaba-server is not inside the Maktaba app (no payload.json beside its bin folder).")
}

// stepVenv builds (or checks) the pipeline's venv from the payload's wheel and
// constraints with the payload's Python (internal/appvenv), relaying the
// bootstrap's progress as one bar out of 1000. The full log is <logs>/venv.log.
func stepVenv(s *setupRun, _ json.RawMessage) (any, error) {
	payload, err := setupPayload()
	if err != nil {
		return nil, err
	}
	logDir := config.LogsDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(logDir, "venv.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	res, err := appvenv.Run(s.ctx, appvenv.Spec{
		Payload: payload,
		Venv:    filepath.Join(s.cfg.StateDir(), "venv"),
		Log:     io.MultiWriter(logFile, s.log),
	}, func(p appvenv.Progress) {
		// the bootstrap's progress is 0..1 over the whole run and never goes back
		s.progress("venv", p.Message, uint64(p.Fraction*1000), 1000)
	})
	var ve *appvenv.Error
	switch {
	case err == nil:
		return map[string]any{"python": res.Python, "pipeline": res.Pipeline, "reused": res.Reused}, nil
	case errors.Is(err, appvenv.ErrInterrupted):
		return nil, failStep("interrupted", "", "The install stopped; it goes on from where it was next time.")
	case errors.As(err, &ve):
		return nil, failStep("venv_failed", "", "%s (the full log: %s)", ve.Message, logPath)
	}
	return nil, failStep("venv_failed", "", "%v", err)
}
