// Package llmserver runs the local language model server Maktaba.app bundles
// (Ollama) for the pipeline's LLM stages (check-tags, check-clips, enrich,
// display titles, the cluster's LLM door).
//
// The supervisor runs `ollama serve` as its ollama role: loopback only, on a
// port of its own (default 11533, so a user's own Ollama keeps 11434), its
// models under <state>/models/ollama, its log in the logs directory. Every role
// gets its address as MAKTABA_OLLAMA_URL, which the pipeline reads for the
// model server of its rules (a server set in the rules still wins). The
// configured models are downloaded by setup and, when one is missing, by the
// supervisor in the background (Manager); their state is served beside
// /processes for the menu bar and the dependency watcher.
package llmserver

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// DefaultPort is the bundled server's loopback port (ports.LLM): off Ollama's
// 11434, so a user's own Ollama and Maktaba's never meet.
var DefaultPort = ports.LLM.Port

const (
	// DefaultModel is the tag check's model (pipeline rules.tagcheck.model).
	DefaultModel = "qwen2.5:7b"
	// LogName is the server's log file in the logs directory.
	LogName = "ollama.log"
)

// Server is one install's bundled model server.
type Server struct {
	Bin    string   // the ollama executable
	Port   int      // its loopback port
	Models string   // OLLAMA_MODELS: <state>/models/ollama
	Logs   string   // the log file
	Want   []string // the models it keeps
	Pull   bool     // the supervisor downloads a missing model
	// PortSet: [llm] port is in server.toml, so Claim never moves it.
	PortSet bool
}

// Models is the configured model list ([llm] models, else DefaultModel).
func Models(cfg config.Config) []string {
	var out []string
	for _, m := range cfg.LLM.Models {
		if m = strings.TrimSpace(m); m != "" && !contains(out, m) {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		out = []string{DefaultModel}
	}
	return out
}

// ExternalURL is [llm] url (an Ollama elsewhere), trimmed, or "".
func ExternalURL(cfg config.Config) string {
	return strings.TrimRight(strings.TrimSpace(cfg.LLM.URL), "/")
}

// Resolve reports whether this install runs the bundled model server, and how.
// exe is the running maktaba-server (the payload keeps Ollama at
// <payload>/ollama/ollama beside <payload>/bin/maktaba-server).
//
// [llm] enabled = "off" never runs it, and neither does an [llm] url (a server
// elsewhere); "on" runs it whenever the binary is found (an error when not);
// "auto" (the default) runs it when the binary is found.
func Resolve(cfg config.Config, exe string) (Server, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.LLM.Enabled))
	if mode == "off" || ExternalURL(cfg) != "" {
		return Server{}, false, nil
	}
	bin := findBinary(cfg, exe)
	if bin == "" {
		if mode == "on" {
			return Server{}, false, errors.New("[llm] enabled = \"on\" but no ollama binary was found " +
				"(set [llm] binary or MAKTABA_OLLAMA_BIN)")
		}
		return Server{}, false, nil
	}
	port := cfg.LLM.Port
	if port <= 0 {
		port = DefaultPort
	}
	return Server{
		Bin:     bin,
		Port:    port,
		Models:  filepath.Join(cfg.StateDir(), "models", "ollama"),
		Logs:    filepath.Join(config.LogsDir(), LogName),
		Want:    Models(cfg),
		Pull:    !strings.EqualFold(strings.TrimSpace(cfg.LLM.Pull), "off"),
		PortSet: cfg.LLM.Port > 0,
	}, true, nil
}

// findBinary: $MAKTABA_OLLAMA_BIN, [llm] binary, else the payload's
// ollama/ollama ($MAKTABA_APP_BUNDLE, or beside the bin/ of this executable).
// Never PATH: a Homebrew Ollama is the user's, not Maktaba's.
func findBinary(cfg config.Config, exe string) string {
	for _, b := range []string{os.Getenv("MAKTABA_OLLAMA_BIN"), cfg.LLM.Binary} {
		if b = strings.TrimSpace(b); b != "" {
			if isExecutable(b) {
				return b
			}
			return ""
		}
	}
	if p := config.AppBundle(); p != "" && isExecutable(filepath.Join(p, "ollama", "ollama")) {
		return filepath.Join(p, "ollama", "ollama")
	}
	if exe == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	b := filepath.Join(filepath.Dir(filepath.Dir(exe)), "ollama", "ollama")
	if isExecutable(b) {
		return b
	}
	return ""
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// Addr is the loopback address it listens on.
func (s Server) Addr() string { return "127.0.0.1:" + strconv.Itoa(s.Port) }

// URL is the address every role gets (MAKTABA_OLLAMA_URL).
func (s Server) URL() string { return "http://" + s.Addr() }

// Env is the server's own environment, on top of the supervisor's.
//
// OLLAMA_NOPRUNE keeps a half-downloaded model across restarts (the next pull
// resumes it; a start would otherwise prune the partial blobs).
func (s Server) Env() []string {
	return []string{
		"OLLAMA_HOST=" + s.Addr(),
		"OLLAMA_MODELS=" + s.Models,
		"OLLAMA_NOPRUNE=1",
	}
}

// Claim settles the port before serve hands the URL to the roles: an [llm]
// port set in server.toml is used as it is; the default moves to a free
// loopback port when another program holds it. free reports whether an address
// can be bound (tests replace it).
func (s *Server) Claim(free func(addr string) bool) error {
	if s.PortSet || free(s.Addr()) {
		return nil
	}
	p, err := FreePort()
	if err != nil {
		return fmt.Errorf("the model server's port %d is taken and no free port was found: %w", s.Port, err)
	}
	s.Port = p
	return nil
}

// CanBind reports whether addr can be listened on now.
func CanBind(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// FreePort is a loopback port free right now.
func FreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// existingParent is the nearest existing folder at or above p.
func existingParent(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		up := filepath.Dir(p)
		if up == p {
			return p
		}
		p = up
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
