package supervisor

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
)

// RoleOllama is the language model server bundled with Maktaba.app: `ollama
// serve` on its own loopback port, restarted like every role. The pipeline's
// LLM stages reach it through MAKTABA_OLLAMA_URL (ChildEnv).
const RoleOllama Role = "ollama"

// ollamaLogMax: a log larger than this is moved to ollama.log.1 when the server
// (re)starts, so the logs folder never fills up.
const ollamaLogMax = 50 << 20

// BuildOllama constructs the ollama role's process for a resolved (and
// claimed) server. Its output goes to its own log in the logs directory.
func BuildOllama(s llmserver.Server) (Process, error) {
	if err := os.MkdirAll(s.Models, 0o700); err != nil {
		return Process{}, err
	}
	cmd := exec.Command(s.Bin, "serve")
	cmd.Env = append(os.Environ(), s.Env()...)
	wire(cmd)
	if s.Logs != "" {
		if info, err := os.Stat(s.Logs); err == nil && info.Size() > ollamaLogMax {
			_ = os.Rename(s.Logs, s.Logs+".1")
		}
		f, err := config.OpenLogFile(s.Logs) // the user's only, as every log
		if err != nil {
			return Process{}, err
		}
		// kept open for the supervisor's life: every restart appends to it
		cmd.Stdout, cmd.Stderr = f, f
	}
	return Process{Role: RoleOllama, Cmd: cmd, Probe: s.URL() + "/api/version"}, nil
}

// LLMURL is the language model server this machine's roles use: the bundled
// one serve started ([llm] RuntimeURL), else [llm] url, else "" (the pipeline's
// own default then applies).
func LLMURL(cfg config.Config) string {
	if u := strings.TrimRight(strings.TrimSpace(cfg.LLM.RuntimeURL), "/"); u != "" {
		return u
	}
	return llmserver.ExternalURL(cfg)
}

// WithLLM serves the model server's state (llmserver.Manager) in the snapshot
// (its "llm" field) and at GET /llm.
func WithLLM(status func() llmserver.Status) Option {
	return func(s *Supervisor) { s.llm = status }
}

// llmHandler: GET /llm, the model server's state; 200 when it is ready (up,
// every configured model there), 503 otherwise, 404 when this machine has none.
func (s *Supervisor) llmHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.llm == nil {
		http.Error(w, "no language model server on this machine", http.StatusNotFound)
		return
	}
	st := s.llm()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !st.Ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(st)
}
