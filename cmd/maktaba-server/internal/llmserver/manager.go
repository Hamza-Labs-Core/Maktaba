package llmserver

import (
	"context"
	"fmt"
	"io"
	neturl "net/url"
	"strings"
	"sync"
	"time"
)

// Model states in a Status.
const (
	ModelReady       = "ready"       // the server has it
	ModelDownloading = "downloading" // being pulled (Done / Total bytes)
	ModelMissing     = "missing"     // not there, and nothing pulls it ([llm] pull = "off")
	ModelFailed      = "failed"      // the last pull failed (Error); retried
	ModelUnknown     = "unknown"     // the server has not answered yet
)

// Status is the model server's state, served by the supervisor beside
// /processes (the menu bar, the dependency watcher).
type Status struct {
	URL     string `json:"url"`
	Bundled bool   `json:"bundled"` // the app's own server (else [llm] url)
	Up      bool   `json:"up"`      // it answered its last check
	Version string `json:"version,omitempty"`
	// Ready: up, and every configured model is there.
	Ready     bool          `json:"ready"`
	Models    []ModelStatus `json:"models"`
	CheckedAt time.Time     `json:"checked_at"`
	Error     string        `json:"error,omitempty"`
}

// ModelStatus is one configured model.
type ModelStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Done  int64  `json:"done,omitempty"`
	Total int64  `json:"total,omitempty"`
	Error string `json:"error,omitempty"`
}

// Manager watches the model server and, when Pull, downloads a configured
// model it does not have.
type Manager struct {
	Client  *Client
	Models  []string
	Pull    bool
	Bundled bool
	Log     io.Writer
	// Retry is the wait after a check that found the server down or a model
	// missing or failed; Recheck the one after all was ready.
	Retry, Recheck time.Duration
	now            func() time.Time

	mu     sync.Mutex
	status Status
}

// NewManager watches the server at url for these models.
func NewManager(url string, models []string, pull, bundled bool, log io.Writer) *Manager {
	m := &Manager{
		Client: NewClient(url), Models: models, Pull: pull, Bundled: bundled, Log: log,
		Retry: 30 * time.Second, Recheck: 5 * time.Minute, now: time.Now,
	}
	m.status = Status{URL: m.Client.URL, Bundled: bundled, Models: m.states(ModelUnknown)}
	return m
}

func (m *Manager) states(state string) []ModelStatus {
	out := make([]ModelStatus, len(m.Models))
	for i, n := range m.Models {
		out[i] = ModelStatus{Name: n, State: state}
	}
	return out
}

// Status is a copy of the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.Models = append([]ModelStatus(nil), s.Models...)
	// served (the supervisor's /llm and /processes, the System health page) and
	// logged: an [llm] url's login or api_key never with it
	raw := s.URL
	s.URL = RedactURL(raw)
	s.Error = scrubURL(s.Error, raw)
	for i := range s.Models {
		s.Models[i].Error = scrubURL(s.Models[i].Error, raw)
	}
	return s
}

// RedactURL is a model server's URL without its userinfo or query (a remote server's
// login, an api_key=…), for a log line or a page.
func RedactURL(s string) string {
	u, err := neturl.Parse(s)
	if err != nil {
		return "…"
	}
	login := u.User != nil
	u.User, u.Fragment = nil, ""
	if u.RawQuery != "" {
		u.RawQuery = "…"
	}
	out := u.String()
	if login {
		out = strings.Replace(out, "://", "://…@", 1)
	}
	return out
}

// scrubURL is msg (an error, which may quote the URL it fetched) without raw's
// password or query.
func scrubURL(msg, raw string) string {
	if msg == "" {
		return msg
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return msg
	}
	if pw, ok := u.User.Password(); ok && pw != "" {
		msg = strings.ReplaceAll(msg, pw, "…")
	}
	if u.RawQuery != "" {
		msg = strings.ReplaceAll(msg, u.RawQuery, "…")
	}
	return msg
}

func (m *Manager) set(f func(*Status)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(&m.status)
}

// Run checks (and pulls) until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		wait := m.Recheck
		if !m.Check(ctx) {
			wait = m.Retry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Check looks once: is the server up, which models does it have; it pulls a
// missing one when Pull. It reports whether everything is ready.
func (m *Manager) Check(ctx context.Context) bool {
	now := m.now()
	v, err := m.Client.Version(ctx)
	if err != nil {
		m.set(func(s *Status) {
			s.Up, s.Ready, s.Version, s.CheckedAt = false, false, "", now
			s.Error = fmt.Sprintf("%v: %v", ErrNotRunning, err)
			for i := range s.Models {
				if s.Models[i].State != ModelFailed {
					s.Models[i] = ModelStatus{Name: s.Models[i].Name, State: ModelUnknown}
				}
			}
		})
		return false
	}
	have, err := m.Client.Have(ctx)
	if err != nil {
		m.set(func(s *Status) { s.Up, s.Ready, s.Version, s.CheckedAt, s.Error = true, false, v, now, err.Error() })
		return false
	}
	ready := true
	for i, name := range m.Models {
		if have[Canonical(name)] {
			m.setModel(i, ModelStatus{Name: name, State: ModelReady})
			continue
		}
		ready = false
		if !m.Pull {
			m.setModel(i, ModelStatus{Name: name, State: ModelMissing})
			continue
		}
		m.setModel(i, ModelStatus{Name: name, State: ModelDownloading})
		m.logf("[llm] downloading %s", name)
		err := m.Client.Pull(ctx, name, func(p Progress) {
			m.setModel(i, ModelStatus{Name: name, State: ModelDownloading, Done: p.Done, Total: p.Total})
		})
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			m.logf("[llm] %s: download failed (trying again later): %v", name, err)
			m.setModel(i, ModelStatus{Name: name, State: ModelFailed, Error: err.Error()})
			continue
		}
		m.logf("[llm] %s is ready", name)
		m.setModel(i, ModelStatus{Name: name, State: ModelReady})
	}
	if !ready {
		// a pull that succeeded made it ready after all
		ready = true
		for _, ms := range m.Status().Models {
			ready = ready && ms.State == ModelReady
		}
	}
	m.set(func(s *Status) { s.Up, s.Ready, s.Version, s.CheckedAt, s.Error = true, ready, v, m.now(), "" })
	return ready
}

func (m *Manager) setModel(i int, ms ModelStatus) {
	m.set(func(s *Status) {
		if i < len(s.Models) {
			s.Models[i] = ms
		}
	})
}

func (m *Manager) logf(format string, args ...any) {
	if m.Log != nil {
		_, _ = fmt.Fprintf(m.Log, format+"\n", args...)
	}
}
