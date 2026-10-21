package devmigrate

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Settings are the stack's, as it runs: the running processes' environment and
// command lines, else this shell's environment, else stack.sh's defaults.
type Settings struct {
	DatabaseURL string
	PathMap     string
	Workers     int // transcription workers running (0: none seen)
	AudioHours  float64
	GPUGB       float64
	GPUCacheGB  float64
	MinFreePct  float64
	Model       string // MAKTABA_STT_MODEL ("" the default)
	Refine      string // "on", "off" or "" (the default)
	RefineModel string
	EmbedModel  string

	SecretsKey string // the API's and the pipeline's secrets key file
	StreamKey  string // the stream signing key file
	ChromaDir  string
	CacheRoot  string // the streaming cache
	HFHome     string // the Hugging Face cache (hub/ inside)
	CamelData  string // CAMeL Tools' data
}

// The pipeline's default models (pipeline/src/maktaba_pipeline: stt/settings.py,
// stt/cohere.py, search/semantic.py).
const (
	defaultWhisper  = "mlx-community/whisper-large-v3-turbo"
	defaultRefine   = "CohereLabs/cohere-transcribe-arabic-07-2026"
	defaultEmbedder = "intfloat/multilingual-e5-small"
)

// ReadSettings resolves the stack's settings.
func ReadSettings(sys Sys, st Stack) Settings {
	// the workers' first (they get the STT settings), then the api's, streaming's
	var envs []map[string]string
	for _, kind := range []string{"stt", "worker", "api", "streaming"} {
		for _, p := range st.Procs {
			if p.Kind == kind {
				envs = append(envs, p.Env)
			}
		}
	}
	get := func(key, def string) string {
		for _, e := range envs {
			if v := strings.TrimSpace(e[key]); v != "" {
				return v
			}
		}
		if v := strings.TrimSpace(sys.Getenv(key)); v != "" {
			return v
		}
		return def
	}
	arg := func(flag string) string {
		for _, p := range st.Procs {
			if v := p.arg(flag); v != "" {
				return v
			}
		}
		return ""
	}
	num := func(key string, def float64) float64 {
		if f, err := strconv.ParseFloat(get(key, ""), 64); err == nil && f > 0 {
			return f
		}
		return def
	}
	home := sys.Home
	dot := filepath.Join(home, ".maktaba")
	s := Settings{
		DatabaseURL: arg("--database-url"),
		PathMap:     arg("--path-map"),
		Workers:     st.Workers(),
		// stack.sh's defaults (sized for a 96 GB Mac)
		AudioHours:  num("MAKTABA_STT_AUDIO_HOURS", 16),
		GPUGB:       num("MAKTABA_STT_GPU_GB", 20),
		GPUCacheGB:  num("MAKTABA_STT_GPU_CACHE_GB", 2),
		MinFreePct:  num("MAKTABA_STT_MIN_FREE_PCT", 25),
		Model:       get("MAKTABA_STT_MODEL", ""),
		Refine:      onOff(get("MAKTABA_STT_REFINE", "")),
		RefineModel: get("MAKTABA_STT_REFINE_MODEL", ""),
		EmbedModel:  get("MAKTABA_EMBED_MODEL", ""),
		SecretsKey:  get("MAKTABA_SECRETS_KEY", ""),
		StreamKey:   get("MAKTABA_STREAM_SIGNING_KEY", filepath.Join(dot, "stream-signing.key")),
		ChromaDir:   get("MAKTABA_CHROMA_DIR", filepath.Join(dot, "chroma")),
		CacheRoot:   get("MAKTABA_CACHE_ROOT", filepath.Join(dot, "cache", "streaming")),
		HFHome:      get("HF_HOME", filepath.Join(home, ".cache", "huggingface")),
		CamelData:   get("CAMELTOOLS_DATA", filepath.Join(home, ".camel_tools")),
	}
	if s.DatabaseURL == "" {
		s.DatabaseURL = get("MAKTABA_DATABASE_URL", "postgresql://maktaba:maktaba@localhost:5432/maktaba")
	}
	if s.PathMap == "" {
		s.PathMap = get("MAKTABA_PATH_MAP", "/Volumes/Originals/="+home+"/nas-nfs/")
	}
	if s.SecretsKey == "" {
		// the API's default (api/internal/secrets.KeyPath): the XDG config dir
		dir := filepath.Join(home, ".config", "maktaba")
		if x := strings.TrimSpace(sys.Getenv("XDG_CONFIG_HOME")); x != "" {
			dir = filepath.Join(x, "maktaba")
		}
		s.SecretsKey = filepath.Join(dir, "secrets.key")
	}
	return s
}

func onOff(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes":
		return "on"
	case "0", "off", "false", "no":
		return "off"
	}
	return ""
}

// HubRepos are the Hugging Face repos the pipeline loads (Whisper, the second
// listen, the embedder); a model given as a local path is not one.
func (s Settings) HubRepos() []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range []string{orDefault(s.Model, defaultWhisper), orDefault(s.RefineModel, defaultRefine), orDefault(s.EmbedModel, defaultEmbedder)} {
		if strings.Count(m, "/") != 1 || strings.HasPrefix(m, "/") || strings.HasPrefix(m, ".") || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// DB is where the stack's database is: the Docker container's user and database
// (the URL's host and port are the container's published ones).
func (s Settings) DB() (user, database string, err error) {
	u, err := url.Parse(s.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", "", fmt.Errorf("the stack's database URL is not a postgres:// URL")
	}
	user = u.User.Username()
	database = strings.TrimPrefix(u.Path, "/")
	if user == "" || database == "" {
		return "", "", fmt.Errorf("the stack's database URL names no user or database")
	}
	return user, database, nil
}

// Apply carries the settings into server.toml's config: the path map and the
// transcription settings. The database URL is never written (the bundled
// Postgres is used when [database] url is not set).
func (s Settings) Apply(cfg *config.Config) {
	cfg.Media.PathMap = s.PathMap
	t := &cfg.Transcription
	if s.Workers > 0 {
		t.Workers = s.Workers
	}
	t.AudioHours, t.GPUGB, t.GPUCacheGB, t.MinFreePct = s.AudioHours, s.GPUGB, s.GPUCacheGB, s.MinFreePct
	if s.Model != "" {
		t.Model = s.Model
	}
	if s.Refine != "" {
		t.Refine = s.Refine
	}
	if s.RefineModel != "" {
		t.RefineModel = s.RefineModel
	}
	if !cfg.DatabaseURLSet {
		cfg.Database.URL = ""
	}
}

// TOMLLines is what Apply writes, for the plan.
func (s Settings) TOMLLines() []string {
	var c config.Config
	s.Apply(&c)
	t := c.Transcription
	lines := []string{
		fmt.Sprintf("[media] path_map = %q", c.Media.PathMap),
		fmt.Sprintf("[transcription] audio_hours = %g, gpu_gb = %g, gpu_cache_gb = %g, min_free_pct = %g",
			t.AudioHours, t.GPUGB, t.GPUCacheGB, t.MinFreePct),
	}
	if t.Workers > 0 {
		lines = append(lines, fmt.Sprintf("[transcription] workers = %d", t.Workers))
	}
	for _, kv := range [][2]string{{"model", t.Model}, {"refine", t.Refine}, {"refine_model", t.RefineModel}} {
		if kv[1] != "" {
			lines = append(lines, fmt.Sprintf("[transcription] %s = %q", kv[0], kv[1]))
		}
	}
	return lines
}
