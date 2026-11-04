package componentseed

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Disk is where the assets are looked for.
type Disk struct {
	Config     config.Config
	ReleaseDir string // a plain server's release (its <release>/venv), or ""
	Platform   string // darwin-arm64, ...; "" is this machine's
	Ollama     bool   // an ollama binary is there (the payload's, [llm] binary, MAKTABA_OLLAMA_BIN)
	// Python is the pipeline's interpreter as the supervisor runs it
	// (supervisor.PipelinePython: MAKTABA_PYTHON, [pipeline] python, the release's or the
	// app's venv, python3); "" looks in the venvs (<state>/venv, <release>/venv).
	Python string
	Getenv func(string) string
}

// mlModules are the packages of runtime.ml (the stt and search extras): one of them in
// the pipeline's environment means the runtime is there.
var mlModules = []string{"mlx_whisper", "faster_whisper", "sentence_transformers", "chromadb"}

// Platform is this machine's catalog platform.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Assets answers, for each asset of cat, whether it is on disk now, as today's code finds
// each one: the pipeline's environment (runtime.ml, and yt-dlp and the refine dictionary
// that come with it), the model server and its models, the Hugging Face cache.
func Assets(cat *components.Catalog, d Disk) map[string]bool {
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	platform := d.Platform
	if platform == "" {
		platform = Platform()
	}
	state := d.Config.StateDir()
	// the interpreters the pipeline may run: the one the supervisor resolves, else the
	// venvs it would take. A named interpreter (MAKTABA_PYTHON, [pipeline] python) counts
	// only for what its own environment holds: an empty one is not the ML runtime.
	pythons := []string{strings.TrimSpace(d.Python)}
	if pythons[0] == "" {
		pythons = []string{venvBin(filepath.Join(state, "venv"), "python")}
		if d.ReleaseDir != "" {
			pythons = append(pythons, venvBin(filepath.Join(d.ReleaseDir, "venv"), "python"))
		}
	}
	ml, ytdlp := false, false
	for _, py := range pythons {
		env, ok := pythonEnv(py)
		if !ok {
			continue
		}
		ml = ml || env.has(mlModules...)
		ytdlp = ytdlp || env.has("yt_dlp") || exists(filepath.Join(env.bin, filepath.Base(venvBin("", "yt-dlp"))))
	}
	// the pipeline runs MAKTABA_YTDLP instead of its own module when it is set
	// (integrations/youtube_download.py): there when its program is
	if cmd := strings.Fields(getenv("MAKTABA_YTDLP")); len(cmd) > 0 {
		_, err := exec.LookPath(cmd[0])
		ytdlp = err == nil
	}
	out := map[string]bool{}
	for _, c := range cat.Components() {
		if c.Kind != components.KindAsset {
			continue
		}
		var on bool
		switch c.ID {
		case "runtime.ml":
			on = ml
		case "model.refine":
			on = ml // fetched with the ML runtime today
		case "tool.yt-dlp":
			on = ytdlp
		case "runtime.ollama":
			on = d.Ollama
		default:
			src := c.SourceFor(platform)
			switch {
			case strings.HasPrefix(src, "hf:"):
				on = hfCached(d.Config, getenv, strings.TrimPrefix(src, "hf:"))
			case strings.HasPrefix(src, "ollama:"):
				on = ollamaModel(state, strings.TrimPrefix(src, "ollama:"))
			}
		}
		out[c.ID] = on
	}
	return out
}

// pyEnv is a Python environment's folders: its scripts and its site-packages.
type pyEnv struct {
	bin   string
	sites []string
}

// pythonEnv is the environment of the interpreter py (a path, or a name looked up on
// PATH as the supervisor's python3 is). ok is false when py is not there. Read from the
// files alone, never by running it: the packs manager asks every few seconds.
func pythonEnv(py string) (pyEnv, bool) {
	if py == "" {
		return pyEnv{}, false
	}
	if !strings.ContainsAny(py, `/\`) {
		p, err := exec.LookPath(py)
		if err != nil {
			return pyEnv{}, false
		}
		py = p
	}
	if !exists(py) {
		return pyEnv{}, false
	}
	bin := filepath.Dir(py)
	root := filepath.Dir(bin)
	env := pyEnv{bin: bin}
	// POSIX: <root>/lib/python3.X/site-packages; Windows: <root>\Lib\site-packages (a venv's
	// Scripts\python.exe, or an install's python.exe at its root)
	libs, _ := filepath.Glob(filepath.Join(root, "lib", "python3*", "site-packages"))
	env.sites = append(env.sites, libs...)
	for _, r := range []string{root, bin} {
		if p := filepath.Join(r, "Lib", "site-packages"); exists(p) {
			env.sites = append(env.sites, p)
		}
	}
	return env, true
}

// has reports whether one of the modules is installed in the environment.
func (e pyEnv) has(modules ...string) bool {
	for _, site := range e.sites {
		for _, m := range modules {
			if exists(filepath.Join(site, m)) || exists(filepath.Join(site, m+".py")) {
				return true
			}
		}
	}
	return false
}

func venvBin(venv, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", name+".exe")
	}
	return filepath.Join(venv, "bin", name)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func nonEmptyDir(p string) bool {
	e, err := os.ReadDir(p)
	return err == nil && len(e) > 0
}

// hfCached: a repo ("org/name", "@revision" ignored) in the pipeline's Hugging Face cache
// (as the supervisor's child environment sets it: MAKTABA_MODEL_CACHE_DIR, else
// <state>/models; HF_HOME, else <cache>/huggingface), or its copy from the share.
func hfCached(cfg config.Config, getenv func(string) string, repo string) bool {
	repo, _, _ = strings.Cut(repo, "@")
	org, name, ok := strings.Cut(repo, "/")
	if !ok {
		return false
	}
	cache := strings.TrimSpace(getenv("MAKTABA_MODEL_CACHE_DIR"))
	if cache == "" {
		cache = filepath.Join(cfg.StateDir(), "models")
	}
	hfHome := strings.TrimSpace(getenv("HF_HOME"))
	if hfHome == "" {
		hfHome = filepath.Join(cache, "huggingface")
	}
	return nonEmptyDir(filepath.Join(hfHome, "hub", "models--"+org+"--"+name, "snapshots")) ||
		nonEmptyDir(filepath.Join(cache, "share-copies", org+"__"+name))
}

// ollamaModel: a model ("qwen2.5:7b") in the bundled server's store (<state>/models/ollama).
func ollamaModel(state, model string) bool {
	model, _, _ = strings.Cut(model, "@") // a pinned digest ("@sha256:…") is no part of the tag
	name, tag, ok := strings.Cut(model, ":")
	if !ok {
		tag = "latest"
	}
	return exists(filepath.Join(state, "models", "ollama", "manifests", "registry.ollama.ai", "library", name, tag))
}
