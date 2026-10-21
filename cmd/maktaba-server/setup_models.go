package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// defaultRefineModel is the second listen's model when [transcription]
// refine_model is empty (the pipeline's stt/cohere.py DEFAULT_MODEL): a gated
// Hugging Face repo, so setup checks the token can read it.
const defaultRefineModel = "CohereLabs/cohere-transcribe-arabic-07-2026"

// hfBaseURL is Hugging Face (tests point it at a fake).
var hfBaseURL = "https://huggingface.co"

var hfHTTP = &http.Client{
	Timeout: 30 * time.Second,
	// a resolve URL that answers with a redirect to the file's CDN is a yes
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (s *setupRun) modelsDir() string { return filepath.Join(s.cfg.StateDir(), "models") }

// hfTokenPath is where the pipeline's Hugging Face client reads a saved token:
// HF_HOME/token, and HF_HOME is <models>/huggingface (model_dirs.py).
func (s *setupRun) hfTokenPath() string {
	return filepath.Join(s.modelsDir(), "huggingface", "token")
}

func (s *setupRun) refineOn() bool {
	switch strings.ToLower(strings.TrimSpace(s.cfg.Transcription.Refine)) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}

// secondListenHere: refine is on and this machine can run the second listen
// (secondListenOn). Only then is its gated model's sign-in asked and its model
// downloaded: elsewhere Whisper's words are kept.
func (s *setupRun) secondListenHere() bool {
	if !s.refineOn() {
		return false
	}
	var gpus []hostinfo.GPU
	if runtime.GOOS != "darwin" {
		gpus = hostGPUs()
	}
	return secondListenOn(runtime.GOOS, runtime.GOARCH, gpus).Runs
}

func (s *setupRun) refineModel() string {
	if m := strings.TrimSpace(s.cfg.Transcription.RefineModel); m != "" {
		return m
	}
	return defaultRefineModel
}

// stepModels: where the transcription models come from.
//
//	{"source": "huggingface", "hf_token": "hf_…"}  the token is checked (it can
//	                                                read the gated second-listen
//	                                                model) and saved for the pipeline
//	{"source": "nas", "path": "…"}                 the home's published copy is there
//	{"action": "download"}                          (after the venv step) download
//	                                                what the workers load, with progress
func stepModels(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		Action  string `json:"action"`
		Source  string `json:"source"`
		HFToken string `json:"hf_token"`
		Path    string `json:"path"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	if req.Action == "download" {
		return s.downloadModels()
	}
	here := s.secondListenHere()
	switch req.Source {
	case "huggingface":
		token := strings.TrimSpace(req.HFToken)
		if token == "" && here {
			return nil, failStep("hf_token_empty", "hf_token", "Enter a Hugging Face read token.")
		}
		if token != "" {
			if err := checkHFToken(s.ctx, token, s.refineModel(), here); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(s.hfTokenPath()), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(s.hfTokenPath(), []byte(token), 0o600); err != nil {
				return nil, err
			}
		}
		if err := s.updatePending(func(p *setupPending) {
			p.Models = &pendingModels{Source: "huggingface"}
		}); err != nil {
			return nil, err
		}
	case "nas":
		model := s.refineModel() // the gated one, which only the share can stand in for
		if !here {
			model = "" // Whisper and the search model: any copy the home published
		}
		dir, err := findShareCopy(strings.TrimSpace(req.Path), model)
		if err != nil {
			return nil, err
		}
		if err := s.updatePending(func(p *setupPending) {
			p.Models = &pendingModels{Source: "nas", Path: dir}
		}); err != nil {
			return nil, err
		}
	default:
		return nil, failStep("bad_input", "source", "Choose Hugging Face or the NAS copy.")
	}
	if !here {
		return map[string]any{"models": []map[string]string{}}, nil
	}
	return map[string]any{"models": []map[string]string{{"name": "second listen", "model": s.refineModel()}}}, nil
}

// checkHFToken: the token is a valid Hugging Face token and, with the second
// listen on, can read the gated model (its licence accepted on this account).
func checkHFToken(ctx context.Context, token, model string, gated bool) error {
	get := func(method, path string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, method, hfBaseURL+path, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := hfHTTP.Do(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	code, err := get(http.MethodGet, "/api/whoami-v2")
	if err != nil {
		return failStep("network", "", "Hugging Face cannot be reached: %v", err)
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return failStep("hf_token_invalid", "hf_token", "Hugging Face did not accept this token.")
	}
	if code != http.StatusOK {
		return failStep("network", "", "Hugging Face answered %d.", code)
	}
	if !gated {
		return nil
	}
	code, err = get(http.MethodHead, "/"+urlPathEscape(model)+"/resolve/main/config.json")
	if err != nil {
		return failStep("network", "", "Hugging Face cannot be reached: %v", err)
	}
	switch {
	case code >= 200 && code < 400:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return failStep("hf_gated", "hf_token", "Accept the terms of %s on huggingface.co with this account, then try again.", model).with("model", model)
	}
	return failStep("network", "", "Hugging Face answered %d for %s.", code, model)
}

// shareModelFolder is the folder a home publishes a model in on the share
// (stt/model_share.py model_folder).
func shareModelFolder(model string) string { return strings.ReplaceAll(model, "/", "__") }

// findShareCopy finds the home's published copy of model under dir: dir is
// the share's models folder (<share>/maktaba/models) or the share itself. A
// copy is a <revision> folder with its manifest.json (written last).
func findShareCopy(dir, model string) (string, error) {
	if dir == "" {
		return "", failStep("nas_path_empty", "path", "Give the folder on the NAS that holds the models.")
	}
	for _, base := range []string{dir, filepath.Join(dir, "maktaba", "models")} {
		folders := []string{shareModelFolder(model)}
		if model == "" { // any model the home published (no second listen here)
			folders = nil
			entries, _ := os.ReadDir(base)
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					folders = append(folders, e.Name())
				}
			}
		}
		for _, folder := range folders {
			if publishedCopy(filepath.Join(base, folder)) {
				return base, nil
			}
		}
	}
	if model == "" {
		return "", failStep("nas_missing", "path", "%s has no copy of the models (the home publishes them in maktaba/models on the share).", dir).
			with("path", dir, "model", "")
	}
	return "", failStep("nas_missing", "path", "%s has no copy of %s (the home publishes it in maktaba/models on the share).", dir, model).
		with("path", dir, "model", model)
}

// publishedCopy: a model's folder on the share holds a revision the home
// finished publishing (its manifest.json written last).
func publishedCopy(folder string) bool {
	revs, err := os.ReadDir(folder)
	if err != nil {
		return false
	}
	for _, r := range revs {
		if !r.IsDir() || strings.HasPrefix(r.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(folder, r.Name(), "manifest.json")); err == nil {
			return true
		}
	}
	return false
}

// modelEvent is one line of `maktaba-pipeline models download --json`
// (contract-venv.md §2).
type modelEvent struct {
	Event      string            `json:"event"`
	Name       string            `json:"name"`
	Model      string            `json:"model"`
	State      string            `json:"state"`
	Bytes      uint64            `json:"bytes"`
	TotalBytes uint64            `json:"total_bytes"`
	Message    string            `json:"message"`
	OK         bool              `json:"ok"`
	Exit       int               `json:"exit"`
	Items      map[string]string `json:"items"`
}

// downloadModels runs the venv's `maktaba-pipeline models download --json`
// (exactly what the workers load) and relays its byte progress.
func (s *setupRun) downloadModels() (any, error) {
	py := supervisor.VenvPython(filepath.Join(s.cfg.StateDir(), "venv"))
	if _, err := os.Stat(py); err != nil {
		return nil, failStep("venv_missing", "", "The transcription tools are not installed yet (the venv step).")
	}
	p, err := s.loadPending()
	if err != nil {
		return nil, err
	}
	args := []string{"-m", "maktaba_pipeline", "models", "download", "--json"}
	if !s.secondListenHere() {
		// no second listen on this machine: not its gated model, nor its dictionary
		args = append(args, "--only", "whisper,embedder")
	}
	if p.Models != nil && p.Models.Source == "nas" && p.Models.Path != "" {
		args = append(args, "--share", p.Models.Path)
	}
	cmd := exec.CommandContext(s.ctx, py, args...)
	cmd.Env = append(os.Environ(), supervisor.ChildEnv(supervisor.RolePipeline, s.cfg)...)
	cmd.Stderr = s.log
	stop := stopGently(cmd) // a stop asks the download, which keeps what it has for the next run
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the models download: %w", err)
	}
	stop.Started()
	var done modelEvent
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var ev modelEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "model":
			if ev.State == "downloading" {
				s.progress("download", "Downloading "+ev.Model, ev.Bytes, ev.TotalBytes)
			} else {
				s.progress("download", ev.Model+": "+ev.State, 0, 0)
			}
		case "done":
			done = ev
		}
	}
	err = cmd.Wait()
	stop.Close()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	switch {
	case err == nil && done.Event == "done":
		return map[string]any{"models": done.Items}, nil
	case code == 3:
		return nil, failStep("hf_gated", "", "The second-listen model needs a Hugging Face sign-in that accepted its terms (or the NAS copy).").
			with("model", s.refineModel())
	case errors.Is(s.ctx.Err(), context.Canceled):
		return nil, failStep("interrupted", "", "The download stopped; it goes on from where it was next time.")
	}
	return nil, failStep("download_failed", "", "Downloading the models failed (exit %d); the details are in the log.", code)
}

// urlPathEscape is url.PathEscape for a model id's two parts.
func urlPathEscape(model string) string {
	org, name, _ := strings.Cut(model, "/")
	return url.PathEscape(org) + "/" + url.PathEscape(name)
}
