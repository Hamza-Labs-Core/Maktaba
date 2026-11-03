package componentseed

import (
	"os"
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
	Getenv     func(string) string
}

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
	venvs := []string{filepath.Join(state, "venv")}
	if d.ReleaseDir != "" {
		venvs = append(venvs, filepath.Join(d.ReleaseDir, "venv"))
	}
	ml := strings.TrimSpace(getenv("MAKTABA_PYTHON")) != "" || strings.TrimSpace(d.Config.Pipeline.Python) != ""
	ytdlp := false
	for _, v := range venvs {
		ml = ml || exists(venvBin(v, "python"))
		ytdlp = ytdlp || exists(venvBin(v, "yt-dlp"))
	}
	out := map[string]bool{}
	for _, c := range cat.Components() {
		if c.Kind != components.KindAsset {
			continue
		}
		var on bool
		switch {
		case c.ID == "runtime.ml":
			on = ml
		case c.ID == "model.refine":
			on = ml // fetched with the ML runtime today
		case c.ID == "tool.yt-dlp":
			on = ytdlp
		case c.ID == "runtime.ollama":
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
